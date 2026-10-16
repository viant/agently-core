package datasource_test

import (
	"bytes"
	"context"
	"encoding/json"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	registry "github.com/viant/agently-core/internal/tool/registry"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/reporting"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
)

type ordinaryMCPProvider struct{ url string }

func (p ordinaryMCPProvider) Names(context.Context) ([]string, error) {
	return []string{"independent"}, nil
}
func (p ordinaryMCPProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{ToolsListVisibility: mcpcfg.ToolsListVisibilityPublic, ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-06-18", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.url}}}}, nil
}

type ordinaryMCPCall struct {
	Name    string
	Args    map[string]any
	RawArgs json.RawMessage
}
type ordinaryMCPFixture struct {
	mu                  sync.Mutex
	methods             []string
	during              func(int)
	denyPage            int
	deniedCalls         int
	structured          bool
	cursorTokenOverride any
	calls               []ordinaryMCPCall
}

func (f *ordinaryMCPFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
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
	f.mu.Lock()
	f.methods = append(f.methods, q.Method)
	f.mu.Unlock()
	if len(q.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result any
	var rpcErr any
	switch q.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "ordinary-test-session")
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "ordinary-fixture", "version": "1"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		tools := []any{}
		for _, name := range []string{"lookup", "offset", "page", "cursor", "scalar", "cursor-number", "cursor-object", "cursor-object-safe", "windows/list", "cursor.number-list"} {
			props := map[string]any{"term": map[string]any{"type": "string"}}
			if name == "offset" || name == "page" {
				if name == "page" {
					props["pageIndex"] = map[string]any{"type": "integer"}
				} else {
					props["skipRows"] = map[string]any{"type": "integer"}
				}
				props["takeRows"] = map[string]any{"type": "integer"}
			}
			if strings.HasPrefix(name, "cursor") {
				props["resumeToken"] = map[string]any{"type": "string"}
				props["batchSize"] = map[string]any{"type": "integer"}
				if name == "cursor-number" {
					props["resumeToken"] = map[string]any{"type": "number"}
				}
				if name == "cursor-object" || name == "cursor-object-safe" {
					props["resumeToken"] = map[string]any{"type": "object", "properties": map[string]any{"ordinal": map[string]any{"type": "number"}, "tag": map[string]any{"type": "string"}}, "required": []string{"ordinal", "tag"}, "additionalProperties": false}
				}
				if name == "cursor-object-safe" {
					props["resumeToken"].(map[string]any)["properties"].(map[string]any)["ordinal"] = map[string]any{"type": "string"}
				}
			}
			tools = append(tools, map[string]any{"name": name, "description": "Independent ordinary tool", "inputSchema": map[string]any{"type": "object", "properties": props, "required": []string{"term"}, "additionalProperties": false}})
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Meta      any            `json:"_meta"`
		}
		if json.Unmarshal(q.Params, &p) != nil {
			rpcErr = map[string]any{"code": -32602, "message": "invalid arguments"}
			break
		}
		allowed := map[string]bool{"term": true}
		if p.Name == "offset" || p.Name == "page" {
			if p.Name == "page" {
				allowed["pageIndex"] = true
			} else {
				allowed["skipRows"] = true
			}
			allowed["takeRows"] = true
		}
		if strings.HasPrefix(p.Name, "cursor") {
			allowed["resumeToken"] = true
			allowed["batchSize"] = true
		}
		if encoded, _ := json.Marshal(p.Meta); strings.Contains(string(encoded), "viant.datly/component") || strings.Contains(string(encoded), "datly") {
			rpcErr = map[string]any{"code": -32602, "message": "producer metadata is not supported"}
			break
		}
		for key := range p.Arguments {
			if !allowed[key] {
				rpcErr = map[string]any{"code": -32602, "message": "additional property: " + key + " for " + p.Name}
			}
		}
		if rpcErr != nil {
			break
		}
		if _, ok := p.Arguments["term"].(string); !ok {
			rpcErr = map[string]any{"code": -32602, "message": "term must be a string"}
			break
		}
		f.mu.Lock()
		var wire struct {
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(q.Params, &wire)
		f.calls = append(f.calls, ordinaryMCPCall{p.Name, p.Arguments, wire.Arguments})
		ordinal, during := len(f.calls), f.during
		denied := f.denyPage == ordinal
		if denied {
			f.deniedCalls++
		}
		f.mu.Unlock()
		if denied {
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "403 Forbidden"}}}
			break
		}
		if during != nil {
			during(ordinal)
		}
		var payload any
		switch p.Name {
		case "lookup", "windows/list", "cursor.number-list":
			payload = map[string]any{"reply": map[string]any{"widgets": []any{map[string]any{"code": "alpha", "label": p.Arguments["term"]}, map[string]any{"code": "beta", "label": "second"}}}}
		case "offset", "page":
			skip, _ := p.Arguments["skipRows"].(float64)
			take, _ := p.Arguments["takeRows"].(float64)
			if p.Name == "page" {
				page, _ := p.Arguments["pageIndex"].(float64)
				skip = (page - 1) * take
			}
			if take <= 0 {
				rpcErr = map[string]any{"code": -32602, "message": "takeRows is required"}
				break
			}
			items := []any{}
			for n := int(skip); n < int(skip+take) && n < 6; n++ {
				items = append(items, map[string]any{"id": n})
			}
			payload = map[string]any{"reply": map[string]any{"widgets": items}, "nav": map[string]any{"recordCount": 6}}
			if p.Arguments["term"] == "explicitEOF" {
				payload = map[string]any{"reply": map[string]any{"widgets": items}, "nav": map[string]any{"remaining": skip+take < 6}}
			}
		case "cursor":
			token, _ := p.Arguments["resumeToken"].(string)
			next := "after-alpha"
			id := "alpha"
			if token == "after-alpha" {
				next = ""
				id = "beta"
			}
			payload = map[string]any{"reply": map[string]any{"widgets": []any{map[string]any{"id": id}}}, "nav": map[string]any{"nextToken": next, "remaining": next != ""}}
			if p.Arguments["term"] == "repeat" {
				payload = map[string]any{"reply": map[string]any{"widgets": []any{map[string]any{"id": id}}}, "nav": map[string]any{"remaining": true, "nextToken": "after-alpha"}}
			}
			if p.Arguments["term"] == "emptyMore" {
				payload = map[string]any{"reply": map[string]any{"widgets": []any{}}, "nav": map[string]any{"remaining": true, "nextToken": "after-alpha"}}
			}
			if p.Arguments["term"] == "pageError" && token != "" {
				rpcErr = map[string]any{"code": -32603, "message": "second page failed"}
			}

		case "cursor-number", "cursor-object", "cursor-object-safe":
			token := interface{}(json.Number("9223372036854775807"))
			if p.Name == "cursor-object" || p.Name == "cursor-object-safe" {
				token = map[string]any{"ordinal": json.Number("9223372036854775807"), "tag": "opaque"}
			}
			if p.Name == "cursor-object-safe" {
				token = map[string]any{"ordinal": "safe-opaque", "tag": "opaque"}
			}
			f.mu.Lock()
			if f.cursorTokenOverride != nil {
				token = f.cursorTokenOverride
			}
			f.mu.Unlock()
			exact, _ := json.Marshal(token)
			var rawArguments map[string]json.RawMessage
			json.Unmarshal(wire.Arguments, &rawArguments)
			_, final := rawArguments["resumeToken"]
			if final && !bytes.Equal(rawArguments["resumeToken"], exact) {
				rpcErr = map[string]any{"code": -32602, "message": "opaque cursor was changed on wire"}
				break
			}
			id := "alpha"
			if final {
				id = "beta"
			}
			payload = map[string]any{"reply": map[string]any{"widgets": []any{map[string]any{"id": id}}}, "nav": map[string]any{"remaining": !final, "nextToken": token}}
		case "scalar":
			payload = map[string]any{"reply": map[string]any{"amount": 42}}
		default:
			rpcErr = map[string]any{"code": -32601, "message": "unknown tool"}
		}
		if rpcErr != nil {
			break
		}
		raw, _ := json.Marshal(payload)
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(raw)}}}
		f.mu.Lock()
		structured := f.structured
		f.mu.Unlock()
		if structured {
			result = map[string]any{"content": []any{}, "structuredContent": payload}
		}
	default:
		rpcErr = map[string]any{"code": -32601, "message": "unsupported method: " + q.Method}
	}
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"jsonrpc": "2.0", "id": q.ID}
	if rpcErr != nil {
		out["error"] = rpcErr
	} else {
		out["result"] = result
	}
	json.NewEncoder(w).Encode(out)
}
func ordinaryMCPStack(t *testing.T) (*ordinaryMCPFixture, *registry.Registry, context.Context) {
	t.Helper()
	f := &ordinaryMCPFixture{}
	peer := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(peer.Close)
	mgr, e := manager.New(ordinaryMCPProvider{peer.URL + "/mcp"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { mgr.CloseConversation("ordinary-test") })
	// The host has authenticated this workspace session. Keep its canonical
	// user distinct from the provider subject so export workers must restore
	// the persisted identity rather than guessing it from the legacy subject.
	ctx := iauth.WithProvider(iauth.WithCanonicalUserID(iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "ordinary-owner"}), "workspace-ordinary-owner"), "ordinary-workspace-session")
	client, e := mgr.Get(ctx, "ordinary-test", "independent")
	if e != nil {
		t.Fatal(e)
	}
	tools, e := client.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 10 {
		t.Fatalf("ordinary tools/list: %v", e)
	}
	r, e := registry.NewWithManager(mgr)
	if e != nil {
		t.Fatal(e)
	}
	return f, r, ctx
}
func ordinaryDescriptor(id, method string) *dsproto.DataSource {
	return &dsproto.DataSource{ID: id, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "independent", Method: method}, DataSource: types.DataSource{Selectors: &types.Selectors{Data: "reply.widgets", DataInfo: "nav"}}}
}
func ordinaryService(t *testing.T, executor dssvc.ToolExecutor, ds *dsproto.DataSource) *dssvc.Service {
	t.Helper()
	store := dssvc.NewMemoryStore()
	store.Put(ds)
	return dssvc.New(dssvc.Options{Store: store, Executor: executor, DisableCache: true})
}

type ordinarySDKBackend struct {
	sdk.Backend
	service *dssvc.Service
}

func (b ordinarySDKBackend) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	out, e := b.service.Fetch(ctx, in.ID, in.Inputs, dssvc.FetchOptions{BypassCache: true})
	if e != nil {
		return nil, e
	}
	return &api.FetchDatasourceOutput{Rows: out.Rows, DataInfo: out.DataInfo, Metrics: out.Metrics}, nil
}
func TestOrdinaryMCPDatasourceSDKAndReportUseDeclaredToolWithoutPaging(t *testing.T) {
	f, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("widgets", "lookup")
	service := ordinaryService(t, executor, ds)
	t.Setenv("TMPDIR", t.TempDir())
	peer := httptest.NewServer(sdk.NewHandler(ordinarySDKBackend{service: service}))
	t.Cleanup(peer.Close)
	client, e := sdk.NewHTTP(peer.URL)
	if e != nil {
		t.Fatal(e)
	}
	out, e := client.FetchDatasource(ctx, &api.FetchDatasourceInput{ID: ds.ID, Inputs: map[string]any{"term": "from-sdk"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Rows) != 2 || out.Rows[0]["label"] != "from-sdk" || out.DataInfo != nil {
		t.Fatalf("unexpected ordinary UI datasource projection: %#v", out)
	}
	execute := reporting.NewResourceDatasetExecutor(executor, func(context.Context, *dsproto.DataSource, map[string]any) error { return nil })
	report, e := execute(ctx, identity.ResolvedResource{}, ds, map[string]any{"term": "from-report"})
	if e != nil || len(report.Rows) != 2 || report.Rows[0]["label"] != "from-report" {
		t.Fatalf("ordinary report datasource: %#v %v", report, e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 2 {
		t.Fatalf("expected two actual tools/call requests: %#v", f.calls)
	}
	for _, call := range f.calls {
		if call.Name != "lookup" || len(call.Args) != 1 {
			t.Fatalf("unpaginated tool received extraneous arguments: %#v", call)
		}
	}
	for _, method := range f.methods {
		if strings.HasPrefix(method, "namespaces/") {
			t.Fatalf("ordinary fixture must not require namespaces: %s", method)
		}
	}
}
func TestOrdinaryMCPCustomOffsetExplicitModePreservesBackendPages(t *testing.T) {
	_, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("offset-widgets", "offset")
	raw := []byte(`{"enabled":true,"mode":"offset","size":2,"parameters":{"page":"skipRows","size":"takeRows"},"dataInfoSelectors":{"totalCount":"recordCount"}}`)
	if e := json.Unmarshal(raw, &ds.Paging); e != nil {
		t.Fatal(e)
	}
	service := ordinaryService(t, executor, ds)
	t.Setenv("TMPDIR", t.TempDir())
	peer := httptest.NewServer(sdk.NewHandler(ordinarySDKBackend{service: service}))
	t.Cleanup(peer.Close)
	client, e := sdk.NewHTTP(peer.URL)
	if e != nil {
		t.Fatal(e)
	}
	for _, skip := range []int{0, 4} {
		out, e := client.FetchDatasource(ctx, &api.FetchDatasourceInput{ID: ds.ID, Inputs: map[string]any{"term": "custom-offset", "skipRows": skip, "takeRows": 2}})
		if e != nil {
			t.Fatal(e)
		}
		if len(out.Rows) != 2 || !reflect.DeepEqual(out.Rows[0]["id"], float64(skip)) {
			t.Fatalf("backend page was sliced again: %#v", out)
		}
		want := skip/2 + 1
		if got := out.DataInfo["page"]; !reflect.DeepEqual(got, float64(want)) {
			t.Fatalf("explicit offset mode with arbitrary parameter name: page=%v want=%d", got, want)
		}
	}
}
func TestOrdinaryMCPScalarProjectionUsesAuthoredColumn(t *testing.T) {
	_, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("amount", "scalar")
	if e := json.Unmarshal([]byte(`{"data":"reply.amount","valueColumn":"amount"}`), &ds.Selectors); e != nil {
		t.Fatal(e)
	}
	out, e := ordinaryService(t, executor, ds).Fetch(ctx, ds.ID, map[string]any{"term": "scalar"}, dssvc.FetchOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Rows) != 1 || out.Rows[0]["amount"] != float64(42) {
		t.Fatalf("authored scalar column mapping lost result: %#v", out)
	}
}

func TestOrdinaryMCPCursorIsCallerSuppliedWithoutNumericPaging(t *testing.T) {
	fixture, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("cursor-widgets", "cursor")
	service := ordinaryService(t, executor, ds)
	first, err := service.Fetch(ctx, ds.ID, map[string]any{"term": "cursor", "batchSize": 1}, dssvc.FetchOptions{})
	if err != nil || len(first.Rows) != 1 || first.Rows[0]["id"] != "alpha" || first.DataInfo["nextToken"] != "after-alpha" {
		t.Fatalf("first arbitrary cursor page: %#v %v", first, err)
	}
	second, err := service.Fetch(ctx, ds.ID, map[string]any{"term": "cursor", "batchSize": 1, "resumeToken": first.DataInfo["nextToken"]}, dssvc.FetchOptions{})
	if err != nil || len(second.Rows) != 1 || second.Rows[0]["id"] != "beta" || second.DataInfo["remaining"] != false {
		t.Fatalf("second arbitrary cursor page: %#v %v", second, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) != 2 || len(fixture.calls[0].Args) != 2 || len(fixture.calls[1].Args) != 3 {
		t.Fatalf("cursor tool received synthesized numeric paging: %#v", fixture.calls)
	}
}

func TestOrdinaryMCPSameAliasRemainsBoundToParentAndTarget(t *testing.T) {
	fixture, executor, ctx := ordinaryMCPStack(t)
	first := ordinaryDescriptor("localRows", "lookup")
	second := ordinaryDescriptor("localRows", "offset")
	second.Paging = &types.PagingConfig{Enabled: true, Mode: "offset", Size: 2, Parameters: &types.PagingParameters{Page: "skipRows", Size: "takeRows"}, DataInfoSelectors: &types.DataInfoSelectors{TotalCount: "recordCount"}}
	bundles := map[string][]byte{}
	for uri, definition := range map[string]*dsproto.DataSource{"window://plain/one": first, "window://plain/two": second} {
		raw, _ := json.Marshal(definition)
		raw, err := types.CanonicalWindowDescriptor(raw)
		if err != nil {
			t.Fatal(err)
		}
		window := &types.Window{View: types.View{Content: &types.Container{ID: "list", Binding: types.Binding{DataSourceRef: "localRows"}}}, DataSource: map[string]types.DataSource{"localRows": definition.DataSource}, ResourceDependencies: map[string]string{"localRows": identity.ContentFingerprint(raw)}}
		variant := types.WindowResourceVariant{Window: window, DataSources: map[string]json.RawMessage{"localRows": raw}}
		fp, err := types.WindowVariantFingerprint(variant)
		if err != nil {
			t.Fatal(err)
		}
		bundle, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fp}}, Variants: map[string]types.WindowResourceVariant{fp: variant}})
		if err != nil {
			t.Fatal(err)
		}
		bundles[uri] = bundle
	}
	global := dssvc.NewMemoryStore()
	global.Put(ordinaryDescriptor("localRows", "scalar"))
	service := dssvc.New(dssvc.Options{Store: global, Executor: executor, DisableCache: true, ResolveDefinition: func(_ context.Context, pin identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
		raw, ok := bundles[pin.URI]
		if !ok {
			return nil, identity.ErrResourceDenied
		}
		variant, err := types.SelectWindowResource(raw, target)
		if err != nil {
			return nil, err
		}
		descriptor := variant.DataSources[id]
		if descriptor == nil {
			return nil, identity.ErrResourceDenied
		}
		var ds dsproto.DataSource
		if json.Unmarshal(descriptor, &ds) != nil {
			return nil, identity.ErrResourceDenied
		}
		return &ds, nil
	}})
	for _, item := range []struct {
		uri, method string
		input       map[string]any
	}{{"window://plain/one", "lookup", map[string]any{"term": "first-parent"}}, {"window://plain/two", "offset", map[string]any{"term": "second-parent", "skipRows": 4, "takeRows": 2}}} {
		pin := identity.ResolvedResource{URI: item.uri, ValidUntil: time.Now().Add(time.Minute)}
		output, err := service.Fetch(requestctx.WithResolvedResource(ctx, pin), "localRows", item.input, dssvc.FetchOptions{Target: &types.WindowTarget{}})
		if err != nil || len(output.Rows) != 2 {
			t.Fatalf("parent-specific datasource: %#v %v", output, err)
		}
	}
	pin := identity.ResolvedResource{URI: "window://plain/one", ValidUntil: time.Now().Add(time.Minute)}
	if _, err := service.Fetch(requestctx.WithResolvedResource(ctx, pin), "undeclared", map[string]any{"term": "denied"}, dssvc.FetchOptions{}); err == nil {
		t.Fatal("undeclared alias fell back outside parent bundle")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) != 2 || fixture.calls[0].Name != "lookup" || fixture.calls[1].Name != "offset" {
		t.Fatalf("local alias collided with global/other parent: %#v", fixture.calls)
	}
}

func TestOrdinaryMCPCursorConfiguredPagingPreservesExactOpaqueToken(t *testing.T) {
	fixture, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("cursor-configured", "cursor")
	if err := json.Unmarshal([]byte(`{"enabled":true,"mode":"cursor","size":1,"parameters":{"page":"resumeToken","size":"batchSize"},"dataInfoSelectors":{"hasMore":"remaining","nextCursor":"nextToken"}}`), &ds.Paging); err != nil {
		t.Fatal(err)
	}
	service := ordinaryService(t, executor, ds)
	first, err := service.Fetch(ctx, ds.ID, map[string]any{"term": "cursor", "batchSize": 1}, dssvc.FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.DataInfo["hasMore"] != true || first.DataInfo["nextCursor"] != "after-alpha" {
		t.Fatalf("explicit continuation lost: %#v", first)
	}
	second, err := service.Fetch(ctx, ds.ID, map[string]any{"term": "cursor", "batchSize": 1, "resumeToken": first.DataInfo["nextCursor"]}, dssvc.FetchOptions{})
	if err != nil || second.DataInfo["hasMore"] != false || len(second.Rows) != 1 || second.Rows[0]["id"] != "beta" {
		t.Fatalf("terminal cursor page: %#v %v", second, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) != 2 || len(fixture.calls[0].Args) != 2 || len(fixture.calls[1].Args) != 3 || fixture.calls[1].Args["resumeToken"] != "after-alpha" {
		t.Fatalf("cursor tool received extraneous fields or changed token: %#v", fixture.calls)
	}
}

func TestOrdinaryMCPCacheIsolatedByParentAndSelectedTarget(t *testing.T) {
	fixture, executor, ctx := ordinaryMCPStack(t)
	lookup := ordinaryDescriptor("localRows", "lookup")
	cursor := ordinaryDescriptor("localRows", "cursor")
	scalar := ordinaryDescriptor("localRows", "scalar")
	scalar.Selectors = &types.Selectors{Data: "reply.amount", ValueColumn: "amount"}
	service := dssvc.New(dssvc.Options{Executor: executor, ResolveDefinition: func(_ context.Context, pin identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
		if id != "localRows" {
			return nil, identity.ErrResourceDenied
		}
		if target != nil && target.FormFactor == "desktop" {
			return cursor, nil
		}
		if pin.URI == "window://plain/two" {
			return scalar, nil
		}
		return lookup, nil
	}})
	for _, tc := range []struct {
		uri, factor string
		count       int
	}{{"window://plain/one", "phone", 2}, {"window://plain/two", "phone", 1}, {"window://plain/one", "desktop", 1}, {"window://plain/one", "phone", 2}} {
		pin := identity.ResolvedResource{URI: tc.uri, ValidUntil: time.Now().Add(time.Minute)}
		out, err := service.Fetch(requestctx.WithResolvedResource(ctx, pin), "localRows", map[string]any{"term": "identical"}, dssvc.FetchOptions{Target: &types.WindowTarget{FormFactor: tc.factor}})
		if err != nil || len(out.Rows) != tc.count {
			t.Fatalf("cache crossed parent/target: %s %s %#v %v", tc.uri, tc.factor, out, err)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) != 3 || fixture.calls[0].Name != "lookup" || fixture.calls[1].Name != "scalar" || fixture.calls[2].Name != "cursor" {
		t.Fatalf("expected isolated cached source calls and reuse only same parent/target: %#v", fixture.calls)
	}
}

func TestOrdinaryMCPBoundCacheUsesTrustedCurrentPrincipalAndBypassesMissingUser(t *testing.T) {
	fixture, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("localRows", "lookup")
	ds.Cache = &dsproto.CachePolicy{Scope: dsproto.ScopeGlobal}
	service := dssvc.New(dssvc.Options{Executor: executor, ResolveDefinition: func(context.Context, identity.ResolvedResource, *types.WindowTarget, string) (*dsproto.DataSource, error) {
		return ds, nil
	}})
	pin := identity.ResolvedResource{URI: "window://same/list", AuthorityBinding: "same-tenant", ValidUntil: time.Now().Add(time.Minute)}
	for index, subject := range []string{"verified-alice", "verified-bob", "verified-alice"} {
		trusted := iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: subject})
		trusted = dssvc.WithIdentity(trusted, "client-looking-same-user", "same-conversation")
		out, err := service.Fetch(requestctx.WithResolvedResource(trusted, pin), ds.ID, map[string]any{"term": "same"}, dssvc.FetchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Cache == nil || out.Cache.Hit != (index == 2) {
			t.Fatal("trusted current principal cache partition failed", subject, out.Cache)
		}
	}
	otherTenant := pin
	otherTenant.AuthorityBinding = "other-tenant"
	out, err := service.Fetch(requestctx.WithResolvedResource(iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: "verified-alice"}), otherTenant), ds.ID, map[string]any{"term": "same"}, dssvc.FetchOptions{})
	if err != nil || out.Cache == nil || out.Cache.Hit {
		t.Fatal("same user crossed tenant cache", out, err)
	}
	anonymous := requestctx.WithResolvedResource(dssvc.WithIdentity(context.Background(), "client-looking-user", "only-conversation"), pin)
	for i := 0; i < 2; i++ {
		out, err := service.Fetch(anonymous, ds.ID, map[string]any{"term": "anonymous"}, dssvc.FetchOptions{})
		if err != nil || out.Cache != nil && out.Cache.Hit {
			t.Fatal("missing trusted principal reused canonical cache", out, err)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) < 3 {
		t.Fatal("trusted-user isolation or anonymous bypass did not execute independent requests")
	}
}

func TestOrdinaryMCPExplicitOffsetHasMoreFalseWithFullFinalPageWithoutCounts(t *testing.T) {
	_, executor, ctx := ordinaryMCPStack(t)
	ds := ordinaryDescriptor("offset-eof", "offset")
	ds.Paging = &types.PagingConfig{Enabled: true, Mode: "offset", Size: 2, Parameters: &types.PagingParameters{Page: "skipRows", Size: "takeRows"}, DataInfoSelectors: &types.DataInfoSelectors{HasMore: "remaining"}}
	out, err := ordinaryService(t, executor, ds).Fetch(ctx, ds.ID, map[string]any{"term": "explicitEOF", "skipRows": 4, "takeRows": 2}, dssvc.FetchOptions{})
	if err != nil || len(out.Rows) != 2 || out.DataInfo["hasMore"] != false {
		t.Fatal("full final page inferred continuation", out, err)
	}
	if _, exists := out.DataInfo["totalCount"]; exists {
		t.Fatal("unprovided total count inferred")
	}
}

func TestOrdinaryMCPCatalogKeepsRemoteLiteralNamesAuthoritative(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE", t.TempDir())
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	t.Setenv("AGENTLY_MCP_SERVERS", "independent")

	fixture, executor, ctx := ordinaryMCPStack(t)
	refresh, cancel := context.WithCancel(ctx)
	done := executor.InitializeWithRefreshContext(ctx, refresh)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("catalog refresh did not stop")
		}
	})

	for _, method := range []string{"windows/list", "cursor-number", "cursor.number-list"} {
		if _, found := executor.GetRawDefinitionWithContext(ctx, "independent:"+method); !found {
			t.Fatalf("ordinary actual catalog lookup missing literal %s", method)
		}
		server, literal, found, err := executor.ResolveMCPIdentity(ctx, "independent:"+method)
		if err != nil || !found || server != "independent" || literal != method {
			t.Fatalf("catalog inferred server from literal method %q: %q %q %v %v", method, server, literal, found, err)
		}
		if _, err := executor.Execute(ctx, "independent:"+method, map[string]interface{}{"term": "literal"}); err != nil {
			t.Fatal(err)
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.calls) != 3 {
		t.Fatalf("actual literal dispatches: %#v", fixture.calls)
	}
	for i, method := range []string{"windows/list", "cursor-number", "cursor.number-list"} {
		if fixture.calls[i].Name != method {
			t.Fatalf("literal name changed: %#v", fixture.calls)
		}
	}
}
