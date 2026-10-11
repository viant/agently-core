package events

import (
	"bytes"
	"context"
	"encoding/json"
	httpui "github.com/viant/agently-core/adapter/http/ui"
	identity "github.com/viant/agently-core/protocol/resource"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type frameworkHandoffCatalog struct {
	window *types.Window
	denied bool
}

func (*frameworkHandoffCatalog) UsesResourceResolution() bool { return true }
func (*frameworkHandoffCatalog) AuthzReady() bool             { return true }
func (c *frameworkHandoffCatalog) List(context.Context, *forge.WindowDefinitionListInput) (*forge.WindowDefinitionListOutput, error) {
	return &forge.WindowDefinitionListOutput{}, nil
}
func (c *frameworkHandoffCatalog) Get(_ context.Context, in *forge.WindowDefinitionGetInput) (*forge.WindowDefinitionGetOutput, error) {
	if c.denied || in.WindowID != "chat/new" {
		return nil, identity.ErrResourceDenied
	}
	return &forge.WindowDefinitionGetOutput{WindowID: "chat/new", Definition: c.window}, nil
}
func (c *frameworkHandoffCatalog) RevalidateWindowResource(_ context.Context, key string, pin identity.ResolvedResource, _ *types.WindowTarget) (*identity.ResolvedResource, error) {
	if c.denied || key != "chat/new" || pin != *c.window.Resource {
		return nil, identity.ErrResourceDenied
	}
	copy := pin
	return &copy, nil
}
func (c *frameworkHandoffCatalog) CheckWindowContent(context.Context, string, identity.ResolvedResource, *types.WindowTarget) error {
	if c.denied {
		return identity.ErrResourceDenied
	}
	return nil
}
func TestFrameworkAdmissionGETEstablishesExactReportEventInstance(t *testing.T) {
	catalog := &frameworkHandoffCatalog{window: &types.Window{Resource: &identity.ResolvedResource{ProviderIdentity: "configured-framework", URI: "window://agently/chat-new", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte("owned-framework"))}, AuthorityBinding: "verified-owner", ValidUntil: time.Now().Add(time.Minute)}}}
	type actorKey struct{}
	bridge := forge.NewService(&forge.Config{WindowDefinitions: catalog, NamespaceResolver: func(ctx context.Context) (forge.NamespaceIdentity, error) {
		owner, _ := ctx.Value(actorKey{}).(string)
		if owner == "" {
			owner = "verified-owner"
		}
		return forge.NamespaceIdentity{Namespace: owner, ValidUntil: time.Now().Add(2 * time.Minute)}, nil
	}})
	session := ""
	rpc := func(method string, params map[string]interface{}) {
		body, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": "owned", "method": method, "params": params})
		request := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if session != "" {
			request.Header.Set("Mcp-Session-Id", session)
		}
		response := httptest.NewRecorder()
		bridge.Hub().ServeHTTPRPC(response, request)
		if received := response.Header().Get("Mcp-Session-Id"); received != "" {
			session = received
		}
		var envelope map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &envelope)
		if envelope["error"] != nil {
			t.Fatal("owned bridge handshake failed", envelope["error"])
		}
	}
	rpc("ui.hello", map[string]interface{}{"clientId": "active-client"})
	rpc("ui.snapshot", map[string]interface{}{"clientId": "active-client", "data": map[string]interface{}{"conversationId": "conv-1", "windows": []interface{}{map[string]interface{}{"windowId": "chat/new", "windowKey": "chat/new"}}}})

	svc := New(bridge)
	ctx := runtimerequestctx.WithConversationID(context.Background(), "conv-1")
	event := &RecordInput{Kind: "report.context_updated", Detail: map[string]interface{}{"reportId": "owned-inline"}}
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("anchor without trusted admission unexpectedly recorded")
	}
	handler := httpui.NewEmbeddedHandlerWithAuthorization(t.TempDir(), nil, nil, nil, httpui.WithWindowResourceProvider(bridge))
	get := func(query string) int {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/window/chat/new"+query, nil))
		return r.Code
	}
	if code := get("?applyPermission=true"); code != 200 {
		t.Fatal("original admission failed", code)
	}
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("unbound admission implicitly acquired instance")
	}
	if code := get("?applyPermission=true&windowId=chat/new&clientId=foreign-client"); code < 400 {
		t.Fatal("foreign client bound", code)
	}
	if code := get("?applyPermission=true&windowId=other&clientId=active-client"); code < 400 {
		t.Fatal("arbitrary framework instance bound", code)
	}
	foreign := httptest.NewRecorder()
	handler.ServeHTTP(foreign, httptest.NewRequest(http.MethodGet, "/window/chat/new?applyPermission=true&windowId=chat/new&clientId=active-client", nil).WithContext(context.WithValue(context.Background(), actorKey{}, "foreign-actor")))
	if foreign.Code < 400 {
		t.Fatal("foreign actor reused active client")
	}
	if code := get("?applyPermission=true&windowId=chat/new&clientId=active-client"); code != 200 {
		t.Fatal("trusted instance handoff failed", code)
	}
	var output RecordOutput
	if err := svc.record(ctx, event, &output); err != nil || !output.Recorded || output.Event.WindowID != "chat/new" || output.Event.ClientID != "active-client" || output.Event.Resource == nil {
		t.Fatal("context update did not retain exact admitted pin", err)
	}
	if pin, err := bridge.WindowResource(ctx, "foreign-namespace", "active-client", "chat/new", "chat/new"); err == nil || pin != nil {
		t.Fatal("pin crossed namespace")
	}
	catalog.denied = true
	if err := svc.record(ctx, event, &RecordOutput{}); err == nil {
		t.Fatal("revoked managing authority recorded report event")
	}
}
