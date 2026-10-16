package server

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	expose "github.com/viant/agently-core/protocol/mcp/expose"
	resources "github.com/viant/agently-core/service/resource"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	authtransport "github.com/viant/mcp/client/auth/transport"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type primitiveFixtureActorKey struct{}
type primitiveServerPolicy struct{ actor identity.VerifiedActor }

func (p primitiveServerPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	return identity.ResourceDecision{Candidate: c[0], AuthorityBinding: "fixture", ValidUntil: p.actor.ValidUntil}, nil
}
func TestExposedServerExportsLocalPrimitivesWithoutGatewayRecursion(t *testing.T) {
	actor := identity.VerifiedActor{Issuer: "https://fixture.invalid", Subject: "alice", TenantID: "platform", AccountID: "account", IdentityRevision: "1", ValidUntil: time.Now().Add(time.Minute)}
	uri, _ := identity.ParseResourceURI("intent://platform/deliver/orders")
	raw := json.RawMessage(`{"schemaVersion":1,"intent":{"id":"orders"}}`)
	local, e := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "local-workspace", Actor: func(ctx context.Context) (identity.VerifiedActor, error) {
		if ctx.Value(primitiveFixtureActorKey{}) != true {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return actor, nil
	}, Verify: func(ctx context.Context, a identity.VerifiedActor) error {
		if ctx.Value(primitiveFixtureActorKey{}) != true || a != actor {
			return identity.ErrResourceDenied
		}
		return nil
	}, Authorize: func(_ context.Context, _ identity.VerifiedActor, resource identity.ResourceURI, _ string) error {
		if resource != uri {
			return identity.ErrResourceDenied
		}
		return nil
	}, Validators: map[string]resources.LocalResourceValidator{"intent": func(version int64, body json.RawMessage) error {
		if version != 1 || !json.Valid(body) {
			return identity.ErrResource
		}
		return nil
	}}, Bindings: []resources.LocalResourceBinding{{URI: uri.String(), FormatVersion: 1, Resolver: func(context.Context, identity.VerifiedActor, string) (*identity.ResourceResolver, error) {
		return &identity.ResourceResolver{ProviderIdentity: "local-workspace", Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}, Policy: primitiveServerPolicy{actor}}, nil
	}}}})
	require.NoError(t, e)
	runtime := &executor.Runtime{LocalPrimitiveProvider: local}
	server, e := NewExposedMCPServer(context.Background(), runtime, &expose.ServerConfig{Port: 1}, nil)
	require.NoError(t, e)
	// This fixture middleware verifies a fixed test credential and supplies the
	// trusted actor context. The local provider never reads business arguments.
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			http.Error(w, "unauthorized", 401)
			return
		}
		server.Handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), primitiveFixtureActorKey{}, true)))
	}))
	defer host.Close()
	options := &mcp.ClientOptions{Name: "test", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: host.URL + "/primitives/mcp"}}}
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "fixture"), 10*time.Second)
	defer cancel()
	client, e := mcp.NewClientWithContext(ctx, nil, options)
	require.NoError(t, e)
	defer client.Close()
	listed, e := client.ListTools(ctx, nil)
	require.NoError(t, e)
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
		meta := tool.Meta[primitive.AuthoringExtension].(map[string]any)
		require.Equal(t, "local-workspace", meta["providerIdentity"])
	}
	require.True(t, names["namespaces/list"])
	require.True(t, names["intents/get"])
	require.False(t, names["windows/open"])
	require.False(t, names["intents/stamp"])
	got, e := client.CallTool(ctx, &schema.CallToolRequestParams{Name: "intents/get", Arguments: map[string]any{"uri": uri.String(), "revision": "working"}})
	require.NoError(t, e)
	require.NotNil(t, got.StructuredContent)
	encoded, _ := json.Marshal(got.StructuredContent)
	var result primitive.GetResult
	require.NoError(t, json.Unmarshal(encoded, &result))
	require.Equal(t, uri.String(), result.ResolvedResource.URI)
	require.Equal(t, []byte(raw), result.Resource.DefinitionBytes)
}

type primitiveFixtureAuthTransport struct{}

func (primitiveFixtureAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = copy.Header.Clone()
	copy.Header.Set("Authorization", "Bearer fixture")
	return http.DefaultTransport.RoundTrip(copy)
}
