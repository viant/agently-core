package cohort

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	iauth "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	authsvc "github.com/viant/agently-core/service/auth"
	storefs "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/artifactcohort"
	primitive "github.com/viant/forge/backend/provider/contract"
	identity "github.com/viant/forge/backend/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

type menuIdentityProvider struct{ options *mcpcfg.MCPClient }

func (p menuIdentityProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return p.options, nil
}

// All producer metadata, accounts, and actors are synthetic. The normal ingress
// verifies signed user ID tokens before the cohort's interactive read begins.
func TestCohortMenuUsesRequestIdentityAndKeepsMetadataIdentity(t *testing.T) {
	ctx := context.Background()
	publisher := artifactcohort.NewPublisher()
	artifact, _, err := publisher.Publish(ctx, artifactcohort.RootRef{URI: "navigation://example/main", Kind: "navigation", Version: 1}, json.RawMessage(`{"schemaVersion":1,"source":{"dataSourceRef":{"resource":{"uri":"datasource://example/rows"},"providerIdentity":"example-publisher"}},"applications":[]}`), func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	policy := authz.Resource{Tenant: "example-tenant", Kind: "cohort", ID: "readers", Version: "logical"}
	_, err = publisher.ReplaceCatalog(ctx, []authz.Document{{Resource: policy, Revision: 1, Policies: map[string]authz.Policy{artifactcohort.PolicyAction: {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}}, []artifactcohort.Cohort{{Tenant: "example-tenant", Name: "readers", PolicyResource: policy, Roots: []artifactcohort.RootRef{artifact.Root}}}, publisher.Generation())
	require.NoError(t, err)
	type call struct{ method, credential string }
	var mu sync.Mutex
	var calls []call
	tokens := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configured/mcp" {
			http.Error(w, "wrong configured origin", 400)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string                     `json:"name"`
				Arguments map[string]json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		if len(request.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case schema.MethodServerDiscover:
			result = map[string]any{"resultType": "complete", "cacheScope": "public", "supportedVersions": []string{schema.LatestProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}, "ttlMs": 0}
		case schema.MethodToolsCall:
			credential := r.Header.Get("Authorization")
			mu.Lock()
			calls = append(calls, call{request.Params.Name, credential})
			actor := tokens[credential]
			mu.Unlock()
			var body any
			switch request.Params.Name {
			case "namespaces/get":
				if credential != "Bearer bootstrap-service" {
					http.Error(w, "metadata identity required", 403)
					return
				}
				var namespace string
				if json.Unmarshal(request.Params.Arguments["namespace"], &namespace) != nil || namespace != "example" {
					http.Error(w, "wrong namespace", 400)
					return
				}
				manifest, e := publisher.Snapshot()
				if e != nil {
					http.Error(w, "unavailable", 503)
					return
				}
				body = map[string]any{"sync": map[string]any{"fetchMethod": artifactcohort.MethodFetch, "catalog": manifest}}
			case artifactcohort.MethodFetch:
				if credential != "Bearer bootstrap-service" {
					http.Error(w, "metadata identity required", 403)
					return
				}
				var input artifactcohort.RootRef
				if json.Unmarshal(request.Params.Arguments["root"], &input) != nil {
					http.Error(w, "invalid root", 400)
					return
				}
				value, e := publisher.Get(input)
				if e != nil {
					http.Error(w, "missing", 404)
					return
				}
				body = value
			case "resources/get":
				// The producer owns authentication, including rejection of missing user
				// credentials. A bootstrap service credential cannot read user rows.
				if actor == "" {
					http.Error(w, "user identity required", 403)
					return
				}
				var uri, revision string
				_ = json.Unmarshal(request.Params.Arguments["uri"], &uri)
				_ = json.Unmarshal(request.Params.Arguments["revision"], &revision)
				if uri != artifact.Root.URI || revision != "1" {
					http.Error(w, "wrong admitted root", 400)
					return
				}
				visible, _ := json.Marshal(map[string]any{"schemaVersion": 1, "applications": []map[string]any{{"id": actor, "disabled": true}}})
				body = &primitive.GetResult{Resource: &primitive.ResourceState{URI: artifact.Root.URI, Revision: "1", ContentFingerprint: artifact.Root.Hash}, MenuProjection: &primitive.MenuProjection{SchemaVersion: 1, DefinitionBytes: visible, ContentFingerprint: identity.ContentFingerprint(visible)}}
			default:
				http.Error(w, "unsupported tool", 400)
				return
			}
			encoded, e := json.Marshal(body)
			if e != nil {
				http.Error(w, "invalid result", 500)
				return
			}
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(encoded)}}}
		default:
			http.Error(w, "unsupported protocol", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	configured := &mcpcfg.MCPClient{PrimitiveProviderIdentity: "example-publisher", PrimitiveNamespaces: []string{"example"}, ClientOptions: &mcp.ClientOptions{ProtocolVersion: schema.LatestProtocolVersion, Auth: &mcp.ClientAuth{UseIdToken: true}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL + "/configured/mcp"}}}}
	transport, err := authtransport.New(authtransport.WithTransport(http.DefaultTransport))
	require.NoError(t, err)
	manager, err := mcpmgr.New(menuIdentityProvider{options: configured}, mcpmgr.WithAuthRoundTripper(transport))
	require.NoError(t, err)
	defer manager.CloseConversation("")
	runtime := &executor.Runtime{MCPManager: manager, StateStore: storefs.NewStateStore(t.TempDir()), VerifiedPrincipalResolver: &anonymousInteractiveFacts{}}
	client, notifications, err := NewHostClient(ctx, runtime, "catalog", "example-publisher", configured.Transport.URL, "", func(context.Context) (string, error) { return "bootstrap-service", nil })
	require.NoError(t, err)
	binding, err := PrepareBinding(ctx, runtime, Registration{ConnectionName: "catalog", ProviderIdentity: "example-publisher", Tenant: "example-tenant", Account: "example-account", Client: client, Notifications: notifications})
	require.NoError(t, err)
	defer binding.Close()
	require.NoError(t, binding.Source.Sync(ctx))
	source, ok := binding.Metadata.(interface {
		GetMenuWithParameters(context.Context, string, map[string]any) (*primitive.GetResult, error)
	})
	require.True(t, ok)
	authRuntime, cookie := anonymousUserIngress(t)
	for _, subject := range []string{"allowed-user", "foreign-user", "allowed-user"} {
		request := httptest.NewRequest(http.MethodGet, "http://example.test/read", nil)
		request.AddCookie(cookie(subject))
		response := httptest.NewRecorder()
		authsvc.WithAuthProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := iauth.MCPAuthToken(r.Context(), true)
			require.NotEmpty(t, token)
			mu.Lock()
			tokens["Bearer "+token] = subject
			mu.Unlock()
			result, e := source.GetMenuWithParameters(r.Context(), artifact.Root.URI, map[string]any{"scope": "synthetic"})
			require.NoError(t, e)
			if result != nil {
				require.Contains(t, string(result.MenuProjection.DefinitionBytes), subject)
			}
			require.Equal(t, token, iauth.MCPAuthToken(r.Context(), true), "interactive resolution mutated caller credential")
			w.WriteHeader(http.StatusOK)
		}), authRuntime).ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code)
	}
	// Exercise normal producer rejection when a user no longer has credentials.
	caller := cohortCaller{NamespaceCaller: artifactcohort.NamespaceCaller{Client: client, Namespace: "example"}, manager: manager, connection: "catalog"}
	_, err = caller.ResolveMenu(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "allowed-user"}), artifact.Root, nil)
	require.Error(t, err)
	// Bootstrap remains independent after interactive calls and credential loss.
	_, err = caller.Discover(ctx)
	require.NoError(t, err)
	_, err = caller.Fetch(ctx, artifactcohort.FetchInput{Tenant: "example-tenant", Root: artifact.Root})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	var menus, metadata int
	for _, call := range calls {
		if call.method == "resources/get" {
			menus++
			if call.credential != "" {
				require.NotEmpty(t, tokens[call.credential], "menu used a credential outside the verified request actors")
			}
			require.NotEqual(t, "Bearer bootstrap-service", call.credential)
		} else {
			metadata++
			require.Equal(t, "Bearer bootstrap-service", call.credential)
		}
	}
	require.Equal(t, 4, menus)
	require.GreaterOrEqual(t, metadata, 4)
}
