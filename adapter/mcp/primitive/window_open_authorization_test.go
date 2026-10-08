package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	httpui "github.com/viant/agently-core/adapter/http/ui"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/viant/afs"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/policy"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/authz"
	"github.com/viant/forge/backend/service/meta"
	"github.com/viant/forge/backend/types"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

type openFacts struct {
	revoked *atomic.Bool
	lease   time.Time
}

func (f openFacts) Resolve(context.Context) (authz.Facts, error) {
	roles := []string{"reader"}
	if f.revoked.Load() {
		roles = nil
	}
	return authz.Facts{Subject: "fixture-reader", Issuer: "fixture", Tenant: "fixture", Roles: roles, Entities: []authz.Entity{{Type: "record", ID: "21"}}, ValidUntil: f.lease}, nil
}

type openPolicy struct {
	action   *policy.ActionAuthorizer
	resource authz.Resource
	lease    time.Time
}

func (p openPolicy) SelectRevision(ctx context.Context, r identity.ResourceRef, c []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if r.URI != p.resource.ID || r.Revision != "" && r.Revision != "1" {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	if err := p.action.Authorize(ctx, p.resource, "view", nil, ""); err != nil {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	for _, v := range c {
		if v.Selector() == "1" {
			return identity.ResourceDecision{Candidate: v, AuthorityBinding: "fixture-reader/account/original", ValidUntil: p.lease}, nil
		}
	}
	return identity.ResourceDecision{}, identity.ErrResourceDenied
}

type openSource struct{ raw json.RawMessage }

func (s openSource) Candidates(context.Context, identity.ResourceURI) ([]identity.ResourceCandidate, error) {
	return []identity.ResourceCandidate{{Kind: identity.StampedCandidate, Revision: "1", ContentFingerprint: identity.ContentFingerprint(s.raw)}}, nil
}
func (s openSource) ReadCandidate(context.Context, identity.ResourceURI, identity.ResourceCandidate) (json.RawMessage, error) {
	return s.raw, nil
}

type bootstrapTransport struct {
	calls *atomic.Int32
	wrong *atomic.Bool
}

func (t bootstrapTransport) Execute(context.Context, string, map[string]interface{}) (string, error) {
	t.calls.Add(1)
	id := 21
	if t.wrong.Load() {
		id = 22
	}
	return fmt.Sprintf(`{"rows":[{"recordId":%d}]}`, id), nil
}

// This fixture exercises native MCP tools and an actual UI WebSocket command,
// with a real protected datasource pipeline and synthetic upstream transport.
func TestNativeWindowOpenUsesWholeWindowAndProtectedSelectedRead(t *testing.T) {
	ctx := context.Background()
	var revoked, wrong, afterCommand, projectionWrong, shortLease atomic.Bool
	var fetches, commands atomic.Int32
	lease := time.Now().Add(time.Minute)
	uri := "window://example/records"
	resource := authz.Resource{Kind: "window", ID: uri, Version: "1", Tenant: "fixture"}
	store, err := authz.NewStaticStore([]authz.Document{{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{
		"view": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}},
		"read": {Mode: "protected", EntityType: "record", Rule: &authz.Rule{Kind: "role", Value: "reader"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	facts := openFacts{&revoked, lease}
	acl := &authz.Service{Provider: facts, Store: store}
	gate := func(context.Context, authz.Facts, string, authz.Resource, string, *authz.Entity) (policy.GateResult, error) {
		return policy.GateResult{Allow: true, Revision: "gate-1", ValidUntil: lease}, nil
	}
	account := func(context.Context, authz.Facts) (string, error) { return "account", nil }
	action := &policy.ActionAuthorizer{Service: acl, Account: account, Gate: gate, EntityPermission: func(_ context.Context, _ authz.Facts, e authz.Entity, _ string) (bool, error) {
		return e.ID == "21" && !revoked.Load(), nil
	}}
	descriptor := &dsproto.DataSource{ID: "bootstrap", Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "synthetic", Method: "read"}, DataSource: types.DataSource{Selectors: &types.Selectors{Data: "rows"}}}
	definition := &types.Window{WindowKey: "records", Authorization: &types.AuthorizationSpec{Scope: "resource", Resource: &types.AuthorizationResourceSpec{Type: "record", ID: types.AuthorizationValueSelector{Source: "resource", Selector: "recordId"}}, RequestedCapabilities: []string{"read", "write", "archive"}}, View: types.View{Content: &types.Container{ID: "approved"}}, DataSource: map[string]types.DataSource{"bootstrap": descriptor.DataSource}}
	raw, _ := json.Marshal(definition)
	resolver := &identity.ResourceResolver{Source: openSource{raw}, Policy: openPolicy{action, resource, lease}}
	root := t.TempDir()
	catalog, err := service.NewMetadataWindowCatalog(meta.New(afs.New(), root), root, []service.SavedWindow{{WindowDefinitionSummary: service.WindowDefinitionSummary{WindowID: "records", Title: "Records", ResourceURI: uri}, Key: "never-source-fallback"}}, service.WithWindowResourceResolver(func(context.Context, string) (*identity.ResourceResolver, identity.ResourceRef, error) {
		return resolver, identity.ResourceRef{URI: uri}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	dataStore := datasource.NewMemoryStore()
	dataStore.Put(descriptor)
	ds := datasource.New(datasource.Options{Store: dataStore, Executor: bootstrapTransport{&fetches, &wrong}, DisableCache: true, ResolveResource: func(ctx context.Context, p identity.ResolvedResource) (*identity.ResolvedResource, error) {
		_, fresh, e := resolver.ReadResolved(ctx, p)
		return fresh, e
	}, ResolveDefinition: func(context.Context, identity.ResolvedResource, *types.WindowTarget, string) (*dsproto.DataSource, error) {
		return descriptor, nil
	}, AuthorizeDefinition: func(ctx context.Context, _ *dsproto.DataSource, args map[string]interface{}) error {
		return action.Authorize(ctx, resource, "read", &authz.Entity{Type: "record", ID: fmt.Sprint(args["id"])}, "read")
	}})
	permission := &permittedview.AuthzResolver{Service: acl, Account: account, Version: "mapping-1", Gate: gate, Map: func(_ context.Context, _ string, id int, cap string, global bool) (authz.Resource, string, *authz.Entity, error) {
		if global || cap != "read" {
			return authz.Resource{}, "", nil, authz.ErrDenied
		}
		return resource, "read", &authz.Entity{Type: "record", ID: fmt.Sprint(id)}, nil
	}, EntityPermission: action.EntityPermission}
	admission := &permittedview.OpenAdmission{Runtime: permittedview.NewRuntime(permission), Bootstrap: func(ctx context.Context, _ identity.ResolvedResource, _ *types.Window, p map[string]any) (map[string]any, error) {
		result, e := ds.Fetch(ctx, "bootstrap", map[string]interface{}{"id": p["id"]}, datasource.FetchOptions{})
		if e != nil || result == nil || len(result.Rows) != 1 {
			return nil, identity.ErrResourceDenied
		}
		return result.Rows[0], nil
	}, MatchSelection: func(_ context.Context, _ *types.Window, p, row map[string]any) error {
		if fmt.Sprint(p["id"]) != fmt.Sprint(row["recordId"]) {
			return identity.ErrResourceDenied
		}
		return nil
	}}
	cfg := &service.Config{WindowDefinitions: catalog, WindowOpenAdmission: func(ctx context.Context, p identity.ResolvedResource, w *types.Window, in map[string]any) (*service.WindowOpenDecision, error) {
		r, e := admission.ApplyDecision(ctx, p, w, in)
		if e != nil {
			return nil, e
		}
		return &service.WindowOpenDecision{Window: r.Window, ValidUntil: r.ExpiresAt}, nil
	}}
	svc := service.NewService(cfg)
	server := httptest.NewServer(http.HandlerFunc(svc.Hub().ServeWS))
	defer server.Close()
	socket, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err = socket.WriteJSON(map[string]any{"type": "ui.hello", "clientId": "fixture-ui"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for len(svc.Hub().ListClients("default")) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	go func() {
		for {
			var command struct {
				ID     string         `json:"id"`
				Params map[string]any `json:"params"`
			}
			if socket.ReadJSON(&command) != nil {
				return
			}
			options, _ := command.Params["options"].(map[string]any)
			metadata, _ := options["inlineMetadata"].(map[string]any)
			view, _ := metadata["view"].(map[string]any)
			content, _ := view["content"].(map[string]any)
			snapshot, _ := metadata["authorizationSnapshot"].(map[string]any)
			resources, _ := snapshot["resources"].(map[string]any)
			row, _ := resources["21"].(map[string]any)
			caps, _ := row["capabilities"].(map[string]any)
			if content["id"] != "approved" || caps["read"] != true || caps["write"] != false || caps["archive"] != false {
				projectionWrong.Store(true)
			}
			commands.Add(1)
			if shortLease.Load() {
				time.Sleep(15 * time.Millisecond)
			}
			if afterCommand.Load() {
				revoked.Store(true)
			}
			_ = socket.WriteJSON(map[string]any{"id": command.ID, "ok": true, "result": map[string]any{"windowId": "records-instance"}})
		}
	}()
	value, err := NewHandler(svc)(ctx, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := value.(*Handler)
	handler.ClientInitialize = &schema.InitializeRequestParams{ProtocolVersion: "2025-06-18"}
	call := func(name string, args map[string]any) (*schema.CallToolResult, *jsonrpc.Error) {
		return handler.CallTool(ctx, &jsonrpc.TypedRequest[*schema.CallToolRequest]{Request: &schema.CallToolRequest{Params: schema.CallToolRequestParams{Name: name, Arguments: args}}})
	}
	args := func(key string, id int) map[string]any {
		return map[string]any{"clientId": "fixture-ui", "windowKey": key, "resource": map[string]any{"uri": uri, "revision": "1"}, "parameters": map[string]any{"id": id, "roles": []string{"admin"}}, "options": map[string]any{"inlineMetadata": map[string]any{"view": "forged"}}, "windowData": `{"recordId":21,"read":true}`}
	}
	httpHandler := httpui.NewEmbeddedHandlerWithAuthorization(root, nil, nil, permittedview.NewRuntime(permission), httpui.WithWindowResourceProvider(svc))
	query := url.Values{"applyPermission": {"true"}, "windowParams": {`{"id":21,"roles":["admin"]}`}, "resource": {`{"recordId":22,"read":true}`}}
	recorder := httptest.NewRecorder()
	httpHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/window/records?"+query.Encode(), nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"read":true`) || !strings.Contains(recorder.Body.String(), `"write":false`) {
		t.Fatalf("trusted HTTP bootstrap: %d %s", recorder.Code, recorder.Body.String())
	}
	query.Set("windowParams", `{"id":22}`)
	recorder = httptest.NewRecorder()
	httpHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/window/records?"+query.Encode(), nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("caller resourceData granted foreign HTTP row: %d", recorder.Code)
	}
	for _, key := range []string{"records", uri} {
		if _, e := call("window-get", map[string]any{"windowId": key}); e != nil {
			t.Fatalf("direct metadata lookup %s: %v", key, e)
		}
	}
	for _, key := range []string{"records", uri} {
		if _, e := call("forgeWindowOpen", args(key, 21)); e != nil {
			t.Fatalf("authorized direct %s open: %v", key, e)
		}
	}
	if projectionWrong.Load() {
		t.Fatal("caller metadata/optional write capability replaced trusted projection")
	}
	if commands.Load() != 2 || fetches.Load() < 6 {
		t.Fatalf("pipeline/command counts %d/%d", fetches.Load(), commands.Load())
	}
	if _, e := call("forgeWindowOpen", args(uri, 22)); e == nil {
		t.Fatal("foreign selected ID bypassed protected bootstrap")
	}
	wrong.Store(true)
	if _, e := call("forgeWindowOpen", args(uri, 21)); e == nil {
		t.Fatal("mismatched authorized bootstrap row released")
	}
	wrong.Store(false)
	if commands.Load() != 2 {
		t.Fatal("denied selection reached UI")
	}
	originalAdmission := cfg.WindowOpenAdmission
	cfg.WindowOpenAdmission = func(ctx context.Context, p identity.ResolvedResource, w *types.Window, in map[string]any) (*service.WindowOpenDecision, error) {
		decision, err := originalAdmission(ctx, p, w, in)
		if err == nil && shortLease.Load() {
			decision.ValidUntil = time.Now().Add(5 * time.Millisecond)
		}
		return decision, err
	}
	shortLease.Store(true)
	if _, e := call("forgeWindowOpen", args(uri, 21)); e == nil {
		t.Fatal("expired original entity-admission lease refreshed on release")
	}
	shortLease.Store(false)
	afterCommand.Store(true)
	if _, e := call("forgeWindowOpen", args(uri, 21)); e == nil {
		t.Fatal("revoked role released open response")
	}
	if _, e := call("window-get", map[string]any{"windowId": uri}); e == nil {
		t.Fatal("revoked direct URI get served")
	}
	result, e := call("window-list", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	rawResult, _ := json.Marshal(result)
	if strings.Contains(string(rawResult), `\"windowId\":\"records\"`) {
		t.Fatal("revoked window stayed listed")
	}
	admission.Bootstrap = nil
	revoked.Store(false)
	afterCommand.Store(false)
	if _, e := call("forgeWindowOpen", args(uri, 21)); e == nil {
		t.Fatal("missing trusted bootstrap granted open")
	}
	query.Set("windowParams", `{"id":21}`)
	recorder = httptest.NewRecorder()
	httpHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/window/records?"+query.Encode(), nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("missing HTTP bootstrap: %d", recorder.Code)
	}
	cfg.WindowOpenAdmission = nil
	revoked.Store(false)
	afterCommand.Store(false)
	if _, e := call("forgeWindowOpen", args(uri, 21)); e == nil {
		t.Fatal("missing protected admission callback granted open")
	}
}
