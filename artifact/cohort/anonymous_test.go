package cohort

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	mcpmgr "github.com/viant/agently-core/protocol/mcp/manager"
	authsvc "github.com/viant/agently-core/service/auth"
	storefs "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/authz"
	"github.com/viant/authz/gating"
	"github.com/viant/forge/backend/artifactcohort"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	"github.com/viant/scy"
	"github.com/viant/scy/auth/jwt/signer"
)

type anonymousInteractiveFacts struct{ calls atomic.Int32 }

func (p *anonymousInteractiveFacts) ResolvePrincipal(ctx context.Context) (gating.Principal, error) {
	p.calls.Add(1)
	owner, err := authsvc.LocalStateOwner(ctx)
	if err != nil || owner == nil {
		return gating.Principal{}, authz.ErrDenied
	}
	subject := authsvc.EffectiveUserID(ctx)
	roles := []string{"viewer"}
	account := "example-account"
	if subject == "allowed-user" {
		roles = []string{"reader"}
	}
	if subject == "foreign-user" {
		roles = []string{"reader"}
		account = "foreign-account"
	}
	return gating.Principal{AccountID: account, Facts: authz.Facts{Subject: subject, Tenant: "example-tenant", Issuer: "verified", Roles: roles, ValidUntil: owner.ValidUntil}}, nil
}

func anonymousUserIngress(t *testing.T) (*authsvc.Runtime, func(string) *http.Cookie) {
	t.Helper()
	root := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	private, public := filepath.Join(root, "private.pem"), filepath.Join(root, "public.pem")
	require.NoError(t, os.WriteFile(private, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(public, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600))
	raw, err := json.Marshal(map[string]any{"auth": map[string]any{"enabled": true, "cookieName": "verified_session", "jwt": map[string]any{"enabled": true, "rsa": []string{public}}}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), raw, 0600))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runtime, err := authsvc.NewRuntime(ctx, root, nil)
	require.NoError(t, err)
	s := signer.New(&signer.Config{RSA: scy.NewResource("", private, "")})
	require.NoError(t, s.Init(ctx))
	return runtime, func(subject string) *http.Cookie {
		jwt, err := s.Create(time.Hour, map[string]any{"sub": subject})
		require.NoError(t, err)
		body, err := json.Marshal(map[string]string{"idToken": jwt})
		require.NoError(t, err)
		r := httptest.NewRequest(http.MethodPost, "http://example.test/v1/api/auth/session", bytes.NewReader(body))
		w := httptest.NewRecorder()
		authsvc.WithAuthExtensions(http.NotFoundHandler(), runtime).ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		require.Len(t, w.Result().Cookies(), 1)
		return w.Result().Cookies()[0]
	}
}

func TestAnonymousLocalMetadataColdStartNotificationsAndVerifiedUserAdmission(t *testing.T) {
	_, caller := sidecarReplica(t)
	publisher := caller.publisher
	notify := make(chan struct{}, 1)
	streamReady := make(chan struct{}, 1)
	var authenticated, gets, fetches, legacy atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			authenticated.Add(1)
			http.Error(w, "unexpected credentials", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": ready\n\n"))
			w.(http.Flusher).Flush()
			select {
			case streamReady <- struct{}{}:
			default:
			}
			for {
				select {
				case <-r.Context().Done():
					return
				case <-notify:
					_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/resources/list_changed\",\"params\":{}}\n\n"))
					w.(http.Flusher).Flush()
				}
			}
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string                     `json:"name"`
				Arguments map[string]json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid", 400)
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result any
		switch req.Method {
		case schema.MethodServerDiscover:
			result = map[string]any{"resultType": "complete", "cacheScope": "public", "supportedVersions": []string{schema.LatestProtocolVersion}, "capabilities": map[string]any{"tools": map[string]any{}}, "ttlMs": 0}
		case schema.MethodInitialize:
			w.Header().Set("Mcp-Session-Id", "anonymous-session")
			result = map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "serverInfo": map[string]any{"name": "synthetic", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
		case schema.MethodPing:
			result = map[string]any{}
		case schema.MethodToolsCall:
			var body any
			switch req.Params.Name {
			case "namespaces/get":
				gets.Add(1)
				var namespace string
				_ = json.Unmarshal(req.Params.Arguments["namespace"], &namespace)
				if namespace != "example" {
					http.Error(w, "wrong namespace", 400)
					return
				}
				var sync struct {
					KnownHash string `json:"knownHash"`
				}
				_ = json.Unmarshal(req.Params.Arguments["sync"], &sync)
				manifest, err := publisher.Snapshot()
				if err != nil {
					http.Error(w, "unavailable", 503)
					return
				}
				if sync.KnownHash == manifest.Hash {
					manifest = artifactcohort.Manifest{Hash: manifest.Hash, Unchanged: true}
				}
				body = map[string]any{"sync": map[string]any{"fetchMethod": artifactcohort.MethodFetch, "catalog": manifest}}
			case artifactcohort.MethodFetch:
				fetches.Add(1)
				var root artifactcohort.RootRef
				_ = json.Unmarshal(req.Params.Arguments["root"], &root)
				value, err := publisher.Get(root)
				if err != nil {
					http.Error(w, "missing", 404)
					return
				}
				body = value
			default:
				legacy.Add(1)
				http.Error(w, "unsupported metadata method", 400)
				return
			}
			encoded, err := json.Marshal(body)
			if err != nil {
				http.Error(w, "invalid result", 500)
				return
			}
			result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(encoded)}}, "isError": false}
		default:
			http.Error(w, "unsupported protocol method", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	manager, err := mcpmgr.New(nil)
	require.NoError(t, err)
	endpoint := server.URL + "/mcp"
	configured := &mcpcfg.MCPClient{PrimitiveProviderIdentity: "example-publisher", PrimitiveNamespaces: []string{"example"}, ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: endpoint}}}}
	require.NoError(t, manager.RegisterLocal(context.Background(), "catalog", configured, func(context.Context) (mcpclient.Interface, error) { return nil, nil }))
	principals := &anonymousInteractiveFacts{}
	runtime := &executor.Runtime{MCPManager: manager, StateStore: storefs.NewStateStore(t.TempDir()), VerifiedPrincipalResolver: principals}
	path := filepath.Join(t.TempDir(), "cohort.yaml")
	require.NoError(t, os.WriteFile(path, []byte(fmt.Sprintf("connectionName: catalog\nproviderIdentity: example-publisher\nnamespace: example\nexpectedEndpoint: %s\nexpectedTenant: example-tenant\nexpectedAccount: example-account\n", endpoint)), 0600))
	bindings, err := PrepareLocalBindings(context.Background(), runtime, path)
	require.NoError(t, err)
	require.Len(t, bindings, 1)
	binding := bindings[0]
	defer binding.Close()
	require.NoError(t, binding.Source.Sync(context.Background()))
	require.Zero(t, principals.calls.Load(), "anonymous cold start requested an interactive principal")
	require.NoError(t, binding.Start(context.Background()))
	select {
	case <-streamReady:
	case <-time.After(3 * time.Second):
		t.Fatal("anonymous notification stream was not established")
	}
	_, err = binding.Metadata.Get(context.Background(), "window://example/main")
	require.Error(t, err, "anonymous metadata preload granted anonymous interactive admission")
	authRuntime, cookie := anonymousUserIngress(t)
	read := func(subject string) error {
		var readErr error
		r := httptest.NewRequest(http.MethodGet, "http://example.test/v1/read", nil)
		r.AddCookie(cookie(subject))
		w := httptest.NewRecorder()
		authsvc.WithAuthProtection(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, readErr = binding.Metadata.Get(r.Context(), "window://example/main")
			w.WriteHeader(http.StatusOK)
		}), authRuntime).ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
		return readErr
	}
	require.Eventually(t, func() bool { return read("allowed-user") == nil }, 3*time.Second, time.Millisecond)
	require.Error(t, read("viewer-user"))
	require.Error(t, read("foreign-user"))
	beforeGets, beforeFetches := gets.Load(), fetches.Load()
	require.NoError(t, read("allowed-user"))
	require.Equal(t, beforeGets, gets.Load())
	require.Equal(t, beforeFetches, fetches.Load(), "interactive admission fetched producer metadata")
	oldHash := binding.Source.(interface{ KnownHash() string }).KnownHash()
	_, _, err = publisher.Publish(context.Background(), artifactcohort.RootRef{URI: "window://example/main", Kind: "window", Version: 2}, json.RawMessage(`{"window":"updated"}`), func(context.Context, json.RawMessage) error { return nil })
	require.NoError(t, err)
	notify <- struct{}{}
	require.Eventually(t, func() bool { return binding.Source.(interface{ KnownHash() string }).KnownHash() != oldHash }, 3*time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return read("allowed-user") == nil }, 3*time.Second, time.Millisecond)
	require.Error(t, read("viewer-user"))
	require.Zero(t, authenticated.Load())
	require.Zero(t, legacy.Load())
	require.Greater(t, gets.Load(), beforeGets)
	require.Equal(t, beforeFetches+1, fetches.Load())
}
