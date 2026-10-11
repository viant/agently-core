package sdk

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	iauth "github.com/viant/agently-core/internal/auth"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	"github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
)

type datasourcePeerOptions map[string]*mcpcfg.MCPClient

func (p datasourcePeerOptions) Names(context.Context) ([]string, error) {
	return []string{"public-peer", "private-peer"}, nil
}
func (p datasourcePeerOptions) Options(_ context.Context, n string) (*mcpcfg.MCPClient, error) {
	return p[n], nil
}

type datasourceHTTPPeer struct {
	mu            sync.Mutex
	private       bool
	denied        bool
	sourceHeaders []string
	args          []map[string]any
	dataHeaders   []string
}

func (f *datasourceHTTPPeer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var q struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		w.WriteHeader(400)
		return
	}
	if len(q.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	ns, peer, subject := "public", "public-peer", "public"
	if f.private {
		ns, peer = "private", "private-peer"
		subject = strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), "-rotated")
	}
	wrap := func(v any) any {
		raw, _ := json.Marshal(v)
		return map[string]any{"structuredContent": v, "content": []any{map[string]any{"type": "text", "text": string(raw)}}}
	}
	var value any
	var rpc any
	switch q.Method {
	case "initialize":
		value = map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": peer, "version": "1"}}
	case "ping":
		value = map[string]any{}
	case "tools/list":
		tools := []any{}
		for _, name := range []string{"namespaces/list", "namespaces/get", "datasources/list", "datasources/get", "templates/list", "templates/get", "rows"} {
			entry := map[string]any{"name": name, "inputSchema": map[string]any{"type": "object", "additionalProperties": true}, "annotations": map[string]any{"readOnlyHint": true}}
			if name != "rows" {
				entry["_meta"] = map[string]any{"com.viant.resources/primitive": map[string]any{"version": 1, "providerIdentity": peer, "transport": "tools/call"}}
			}
			tools = append(tools, entry)
		}
		value = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name string         `json:"name"`
			Args map[string]any `json:"arguments"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(q.Params)))
		decoder.UseNumber()
		_ = decoder.Decode(&p)
		f.mu.Lock()
		f.sourceHeaders = append(f.sourceHeaders, r.Header.Get("Authorization"))
		f.mu.Unlock()
		if f.private && subject != "alice" && subject != "bob" {
			value = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "403 Forbidden"}}}
			break
		}
		switch p.Name {
		case "namespaces/list":
			value = wrap(map[string]any{"providerIdentity": peer, "namespaces": []any{map[string]any{"name": ns, "kinds": []string{"datasource", "template"}}}, "complete": true})
		case "namespaces/get":
			value = wrap(map[string]any{"providerIdentity": peer, "namespace": ns, "kinds": []any{map[string]any{"kind": "datasource", "formatVersions": []int{1}, "operations": []string{"list", "get"}, "methods": map[string]any{"list": "datasources/list", "get": "datasources/get"}}, map[string]any{"kind": "template", "formatVersions": []int{1}, "operations": []string{"list", "get"}, "methods": map[string]any{"list": "templates/list", "get": "templates/get"}}}})
		case "templates/list":
			value = wrap(map[string]any{"templates": []any{map[string]any{"uri": "template://" + ns + "/form", "kind": "template", "namespace": ns, "name": "form", "formatVersion": 1}}, "complete": true})
		case "templates/get":
			uri, _ := p.Args["uri"].(string)
			library := json.RawMessage(`{"id":"library","name":"Library","lookupOverlays":[{"id":"accounts","target":{"kind":"template","id":"form"},"bindings":[{"match":{"fieldName":"account"},"named":{"name":"account"},"lookup":{"dataSource":"datasource://private/unique"}}]}]}`)
			if strings.HasSuffix(uri, "/library") && subject != "alice" {
				value = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "403 Forbidden"}}}
				break
			}
			raw := library
			if strings.HasSuffix(uri, "/form") {
				raw, _ = json.Marshal(map[string]any{"id": "form", "name": "Form", "lookupLibraries": []any{map[string]any{"resource": map[string]any{"uri": "template://" + ns + "/library", "revision": "working"}, "providerIdentity": peer, "contentFingerprint": identity.ContentFingerprint(library)}}})
			}
			name := strings.TrimPrefix(uri, "template://"+ns+"/")
			value = wrap(map[string]any{"resource": map[string]any{"uri": uri, "kind": "template", "namespace": ns, "name": name, "formatVersion": 1, "definitionBytes": base64.StdEncoding.EncodeToString(raw), "contentFingerprint": identity.ContentFingerprint(raw)}, "resolvedResource": map[string]any{"providerIdentity": peer, "uri": uri, "kind": "working", "contentFingerprint": identity.ContentFingerprint(raw), "authorityBinding": "provider:" + subject, "validUntil": time.Now().Add(time.Hour).UTC()}})
		case "datasources/list":
			items := []any{}
			for _, name := range []string{"shared", "unique", "composite", "fanout", "static"} {
				items = append(items, map[string]any{"uri": "datasource://" + ns + "/" + name, "kind": "datasource", "namespace": ns, "name": name, "formatVersion": 1})
			}
			value = wrap(map[string]any{"datasources": items, "complete": true})
		case "datasources/get":
			uri, _ := p.Args["uri"].(string)
			name := strings.TrimPrefix(uri, "datasource://"+ns+"/")
			if name != "shared" && name != "unique" && name != "composite" && name != "fanout" && name != "static" {
				rpc = map[string]any{"code": -32602, "message": "unknown source"}
				break
			}
			id := "shared"
			if name == "unique" {
				id = "owned-" + subject
			}
			descriptor := map[string]any{"id": id, "selectors": map[string]any{"data": "reply.rows"}, "backend": map[string]any{"kind": "mcp_tool", "service": peer, "method": "rows", "pinned": map[string]any{"largeDefault": json.Number("9007199254740993")}}}
			if name == "composite" || name == "fanout" {
				call := map[string]any{"service": peer, "method": "rows", "args": map[string]string{"account": "inputs.account", "entityId": "inputs.entityId", "filters": "inputs.filters"}}
				descriptor["id"] = name
				delete(descriptor, "selectors")
				if name == "composite" {
					descriptor["backend"] = map[string]any{"kind": "mcp_tools", "calls": []any{call}}
				} else {
					descriptor["backend"] = map[string]any{"kind": "mcp_fanout", "fanout": map[string]any{"seed": call, "itemsSelector": "data", "call": call, "resultMap": "byId", "resultKey": "item.id", "fieldMap": map[string]string{"id": "result.id"}}}
				}
			}
			if name == "static" {
				descriptor["id"] = "static"
				delete(descriptor, "selectors")
				descriptor["backend"] = map[string]any{"kind": "inline", "rows": []any{map[string]any{"id": "static-row"}}}
			}
			raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "dataSource": descriptor})
			sum := sha256.Sum256(raw)
			fp := fmt.Sprintf("%x", sum[:])
			value = wrap(map[string]any{"resource": map[string]any{"uri": uri, "kind": "datasource", "namespace": ns, "name": name, "formatVersion": 1, "definitionBytes": base64.StdEncoding.EncodeToString(raw), "contentFingerprint": fp}, "resolvedResource": map[string]any{"providerIdentity": peer, "uri": uri, "kind": "working", "contentFingerprint": fp, "authorityBinding": "provider:" + subject, "validUntil": time.Now().Add(time.Hour).UTC()}})
		case "rows":
			f.mu.Lock()
			f.args = append(f.args, p.Args)
			f.dataHeaders = append(f.dataHeaders, r.Header.Get("Authorization"))
			f.mu.Unlock()
			f.mu.Lock()
			denied := f.denied
			f.mu.Unlock()
			if p.Args["account"] != subject || denied {
				value = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "403 Forbidden"}}}
				break
			}
			rows := []any{map[string]any{"id": "row-" + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")}}
			value = wrap(map[string]any{"reply": map[string]any{"rows": rows}, "data": rows, "byId": map[string]any{"row-" + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "): rows[0]}})
		default:
			rpc = map[string]any{"code": -32601, "message": "unknown tool"}
		}
	default:
		rpc = map[string]any{"code": -32601, "message": "unknown method"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "datasource-fixture")
	res := map[string]any{"jsonrpc": "2.0", "id": q.ID}
	if rpc != nil {
		res["error"] = rpc
	} else {
		res["result"] = value
	}
	_ = json.NewEncoder(w).Encode(res)
}

func TestSDKDatasourceGatewayUsesFreshActorDefinitionsAndManagingMCP(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	old, oldRuntime := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(root)
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old); workspace.SetRuntimeRoot(oldRuntime) })
	require.NoError(t, os.MkdirAll(filepath.Join(root, "extension/forge/datasources"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "extension/forge/datasources/decoy.yaml"), []byte("invalid: ["), 0600))
	public, private := &datasourceHTTPPeer{}, &datasourceHTTPPeer{private: true}
	pubServer := httptest.NewServer(http.HandlerFunc(public.serve))
	defer pubServer.Close()
	privServer := httptest.NewServer(http.HandlerFunc(private.serve))
	defer privServer.Close()
	options := datasourcePeerOptions{}
	for name, url := range map[string]string{"public-peer": pubServer.URL, "private-peer": privServer.URL} {
		cfg := &mcpcfg.MCPClient{PrimitiveProviderIdentity: name, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: url}}}}
		if name == "private-peer" {
			pass := true
			cfg.Auth = &mcp.ClientAuth{PassUserToken: &pass, UseIdToken: true}
		}
		options[name] = cfg
	}
	mgr, err := manager.New(options)
	require.NoError(t, err)
	actor := feedActor
	verify := func(ctx context.Context, a identity.VerifiedActor) error {
		current, err := actor(ctx)
		if err != nil || a.Subject != current.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}
	rt, err := executor.NewBuilder().WithMCPManager(mgr).WithPrimitiveAuthority(actor, verify, "host").WithAgentFinder(&streamTestAgentFinder{agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "owned"}}}).WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).Build(context.Background())
	require.NoError(t, err)
	defer rt.Close(context.Background())
	client, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	require.Same(t, rt.PrimitiveProviders, client.primitiveProviders)
	alice := iauth.WithIDToken(feedActorContext("alice"), "alice")
	bob := iauth.WithIDToken(feedActorContext("bob"), "bob")
	listed, err := client.InlineReportWorkspaceDataSources(alice)
	require.NoError(t, err)
	require.Contains(t, listed, "owned-alice")
	require.NotContains(t, listed, "owned-bob")
	require.NotContains(t, listed, "shared", "ambiguous authored alias must not be admitted")
	listed, err = client.InlineReportWorkspaceDataSources(bob)
	require.NoError(t, err)
	require.Contains(t, listed, "owned-bob")
	require.NotContains(t, listed, "owned-alice")
	_, err = client.FetchDatasource(alice, &api.FetchDatasourceInput{ID: "shared", Inputs: map[string]any{"account": "alice"}})
	require.ErrorIs(t, err, resource.ErrCollision)
	input := &api.FetchDatasourceInput{ID: "owned-alice", Inputs: map[string]any{"account": "alice", "entityId": "different-opaque-id", "parent": "other-parent", "filters": map[string]any{"number": json.Number("9007199254740993"), "term": "unchanged"}}, Cache: &api.DatasourceCacheHints{BypassCache: true}}
	before, _ := json.Marshal(input)
	rows, err := client.FetchDatasource(alice, input)
	require.NoError(t, err)
	require.Len(t, rows.Rows, 1)
	require.Equal(t, "row-alice", rows.Rows[0]["id"])
	after, _ := json.Marshal(input)
	require.Equal(t, before, after)
	private.mu.Lock()
	actual := private.args[len(private.args)-1]
	private.mu.Unlock()
	require.Equal(t, json.Number("9007199254740993"), actual["largeDefault"])
	for key, value := range input.Inputs {
		require.Equal(t, value, actual[key])
	}
	rows, err = client.FetchDatasource(bob, &api.FetchDatasourceInput{ID: "owned-bob", Inputs: map[string]any{"account": "bob"}, Cache: &api.DatasourceCacheHints{BypassCache: true}})
	require.NoError(t, err)
	require.Equal(t, "row-bob", rows.Rows[0]["id"])
	rows, err = client.FetchDatasource(alice, &api.FetchDatasourceInput{ID: "datasource://public/unique", Inputs: map[string]any{"account": "public"}, Cache: &api.DatasourceCacheHints{BypassCache: true}})
	require.NoError(t, err)
	require.Equal(t, "row-", rows.Rows[0]["id"])
	rotated := iauth.WithIDToken(feedActorContext("alice"), "alice-rotated")
	rotatedInput := &api.FetchDatasourceInput{ID: "owned-alice", Inputs: input.Inputs}
	rows, err = client.FetchDatasource(rotated, rotatedInput)
	require.NoError(t, err)
	private.mu.Lock()
	t.Logf("rotation managing call count=%d lastCredentialCurrent=%v", len(private.args), private.dataHeaders[len(private.dataHeaders)-1] == "Bearer alice-rotated")
	private.mu.Unlock()
	require.Equal(t, "row-alice-rotated", rows.Rows[0]["id"], "same user changed credential must reach managing MCP")
	private.mu.Lock()
	private.denied = true
	deniedCount := len(private.args)
	private.mu.Unlock()
	_, err = client.FetchDatasource(rotated, rotatedInput)
	require.Error(t, err, "same credential data revocation cannot be replaced by host cached rows")
	private.mu.Lock()
	require.Len(t, private.args, deniedCount+1)
	private.denied = false
	private.mu.Unlock()
	private.mu.Lock()
	count := len(private.args)
	private.mu.Unlock()
	_, err = client.FetchDatasource(alice, &api.FetchDatasourceInput{ID: "owned-alice", Inputs: map[string]any{"account": "foreign"}, Cache: &api.DatasourceCacheHints{BypassCache: true}})
	require.Error(t, err)
	private.mu.Lock()
	require.Len(t, private.args, count+1, "managing own-account denial receives one call without replay")
	private.mu.Unlock()
	for _, variant := range []struct {
		name  string
		calls int
	}{{"composite", 1}, {"fanout", 2}} {
		t.Run(variant.name+"-same-credential-revocation", func(t *testing.T) {
			request := &api.FetchDatasourceInput{ID: "datasource://private/" + variant.name, Inputs: map[string]any{"account": "alice", "entityId": "opaque-other", "filters": map[string]any{"term": "preserved"}}}
			original, _ := json.Marshal(request)
			private.mu.Lock()
			start := len(private.args)
			private.mu.Unlock()
			got, err := client.FetchDatasource(alice, request)
			require.NoError(t, err)
			require.Len(t, got.Rows, 1)
			private.mu.Lock()
			require.Len(t, private.args, start+variant.calls)
			for _, args := range private.args[start:] {
				for key, value := range request.Inputs {
					require.Equal(t, value, args[key])
				}
			}
			private.denied = true
			private.mu.Unlock()
			_, err = client.FetchDatasource(alice, request)
			require.Error(t, err)
			private.mu.Lock()
			require.Len(t, private.args, start+variant.calls+1)
			private.denied = false
			private.mu.Unlock()
			unchanged, _ := json.Marshal(request)
			require.Equal(t, original, unchanged)
		})
	}
	t.Run("inline-cache-retained-and-invalidation-scoped", func(t *testing.T) {
		request := &api.FetchDatasourceInput{ID: "datasource://private/static"}
		first, err := client.FetchDatasource(alice, request)
		require.NoError(t, err)
		require.False(t, first.Cache.Hit)
		second, err := client.FetchDatasource(alice, request)
		require.NoError(t, err)
		require.True(t, second.Cache.Hit)
		_, err = client.FetchDatasource(bob, request)
		require.NoError(t, err)
		publicRequest := &api.FetchDatasourceInput{ID: "datasource://public/static"}
		_, err = client.FetchDatasource(alice, publicRequest)
		require.NoError(t, err)
		require.NoError(t, client.InvalidateDatasourceCache(alice, &api.InvalidateDatasourceCacheInput{ID: request.ID}))
		after, err := client.FetchDatasource(alice, request)
		require.NoError(t, err)
		require.False(t, after.Cache.Hit)
		other, err := client.FetchDatasource(bob, request)
		require.NoError(t, err)
		require.True(t, other.Cache.Hit, "Alice's invalidation must not clear Bob's pin")
		other, err = client.FetchDatasource(alice, publicRequest)
		require.NoError(t, err)
		require.True(t, other.Cache.Hit, "same authored ID in another namespace remains cached")
	})
	t.Run("explicit-lookup-owner-api-and-library-denial", func(t *testing.T) {
		request := &api.ListLookupRegistryInput{Context: "template:form", Owner: &identity.ResourceRef{URI: "template://private/form", Revision: identity.WorkingCandidate}, ProviderIdentity: "private-peer"}
		got, err := client.ListLookupRegistry(alice, request)
		require.NoError(t, err)
		require.Len(t, got.Entries, 1)
		require.Equal(t, "account", got.Entries[0].Name)
		_, err = client.ListLookupRegistry(bob, request)
		require.Error(t, err, "private library provider must authorize Bob anew")
		got, err = client.ListLookupRegistry(alice, &api.ListLookupRegistryInput{Context: "template://private/form"})
		require.NoError(t, err)
		require.Len(t, got.Entries, 1)
		wrong := *request
		wrong.ProviderIdentity = "public-peer"
		_, err = client.ListLookupRegistry(alice, &wrong)
		require.Error(t, err, "provider hint cannot reinterpret owner")
		for _, caller := range []struct {
			ctx    context.Context
			status int
		}{{alice, http.StatusOK}, {bob, http.StatusForbidden}} {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/api/lookups/registry?context=template%3Aform&owner=template%3A%2F%2Fprivate%2Fform&revision=working&providerIdentity=private-peer", nil).WithContext(caller.ctx)
			handleListLookupRegistry(client)(recorder, req)
			require.Equal(t, caller.status, recorder.Code)
		}
		owner, err := client.primitiveProviders.GetForProvider(alice, "private-peer", *request.Owner, nil)
		require.NoError(t, err)
		require.NotNil(t, client.elicSvc)
		rs := &schema.ElicitRequestParamsRequestedSchema{Properties: map[string]interface{}{"account": map[string]interface{}{"type": "string"}}}
		require.NoError(t, client.elicSvc.RefineRequestedSchemaContext(requestctx.WithResolvedResource(alice, *owner.ResolvedResource), rs))
		require.Equal(t, "lookup", rs.Properties["account"].(map[string]interface{})["x-ui-widget"])
		err = client.elicSvc.RefineRequestedSchemaContext(requestctx.WithResolvedResource(bob, *owner.ResolvedResource), rs)
		require.Error(t, err)
		require.Empty(t, client.overlayStore.List(), "private library overlays remain operation-local")
	})
	require.Empty(t, client.datasourceStore.List(), "private definitions must never enter the shared SDK store")
	require.NoError(t, client.InvalidateDatasourceCache(alice, &api.InvalidateDatasourceCacheInput{ID: "owned-alice"}))
	public.mu.Lock()
	for _, header := range public.sourceHeaders {
		require.Empty(t, header, "public MCP requires no Authorization header")
	}
	public.mu.Unlock()
	private.mu.Lock()
	for _, header := range private.sourceHeaders {
		require.Contains(t, []string{"Bearer alice", "Bearer bob", "Bearer alice-rotated"}, header)
	}
	private.mu.Unlock()
}
