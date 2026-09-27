package workspace

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	ws "github.com/viant/agently-core/workspace"
	forgetypes "github.com/viant/forge/backend/types"
	"gopkg.in/yaml.v3"
)

func TestLayoutRejectsHiddenWhenTypo(t *testing.T) {
	input := `version: 1
id: main
applications:
  - id: protected
    title: Protected
    menus:
      - id: one
        title: One
        hiddenWhen: {source: authorization, field: principal.features, equal: secret}
        action: {type: window, windowKey: one}
`
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(input), &node); err != nil {
		t.Fatal(err)
	}
	if err := validateLayoutYAML(&node); err != nil {
		t.Fatal(err)
	}
	// hiddenWhen is intentionally used here: an unknown operator returning false
	// would make a protected item visible if it reached the evaluator.
	var parsed Layout
	if err := node.Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	err := validateLayout(&parsed)
	if err == nil || !strings.Contains(err.Error(), "equal") {
		t.Fatalf("expected unknown operator diagnostic, got %v", err)
	}
}

type layoutResolver struct{ roles, features []string }

type layoutPolicyResolver struct {
	calls   int
	request *policy.Request
}

func (r *layoutPolicyResolver) Resolve(_ context.Context, request *policy.Request) (*policy.Decision, error) {
	r.calls++
	r.request = request
	return &policy.Decision{PolicyVersion: "v1", ExpiresAt: time.Now().Add(time.Minute), Allow: true, AllowedIDs: []string{"one"}}, nil
}

func (r layoutResolver) Resolve(_ context.Context, req *permittedview.Request) (*permittedview.Snapshot, error) {
	if len(req.ResourceIDs) != 0 || !req.IncludePrincipal {
		panic("application navigation requested entity IDs or omitted principal")
	}
	return &permittedview.Snapshot{AuthorizationVersion: "v1", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"roles": r.roles, "features": r.features}}, nil
}

func TestLayoutFiltersAppsByRoleAndFeatureWithoutEntityIDs(t *testing.T) {
	restoreAuth := permittedview.SetDefaultRuntime(&permittedview.Runtime{Resolver: layoutResolver{roles: []string{"operator"}, features: []string{"OPERATIONS_UI"}}})
	restorePolicy := policy.SetDefaultRuntime(nil)
	defer restoreAuth()
	defer restorePolicy()
	condition := map[string]any{"all": []any{
		map[string]any{"source": "authorization", "field": "principal.roles", "contains": "operator"},
		map[string]any{"source": "authorization", "field": "principal.features", "contains": "OPERATIONS_UI"},
	}}
	layout := &Layout{Version: 1, ID: "main", Applications: []LayoutApplication{
		{ID: "operations", Title: "Operations", Authorization: &forgetypes.AuthorizationSpec{ResourceType: "application"}, VisibleWhen: condition, Menus: []LayoutMenu{{ID: "overview", Title: "Overview", Action: &LayoutAction{Type: "window", WindowKey: "overview"}}}},
		{ID: "admin", Title: "Admin", Authorization: &forgetypes.AuthorizationSpec{ResourceType: "application"}, VisibleWhen: map[string]any{"source": "authorization", "field": "principal.roles", "contains": "admin"}, Menus: []LayoutMenu{{ID: "users", Title: "Users", Action: &LayoutAction{Type: "window", WindowKey: "users"}}}},
	}}
	got, err := filterLayout(httptest.NewRequest("GET", "/v1/workspace/layout", nil), layout)
	if err != nil || len(got.Applications) != 1 || got.Applications[0].ID != "operations" {
		t.Fatalf("layout=%+v err=%v", got, err)
	}
	if got.Applications[0].Authorization != nil || got.Applications[0].VisibleWhen != nil {
		t.Fatal("resolved response leaked authorization configuration")
	}
}

func TestLayoutEndpointUsesEmbeddedDefaultAndRejectsInvalidOverride(t *testing.T) {
	previous := ws.Root()
	root := t.TempDir()
	ws.SetRoot(root)
	defer ws.SetRoot(previous)
	restorePolicy := policy.SetDefaultRuntime(nil)
	defer restorePolicy()
	h := NewMetadataHandler(nil, nil, "test")
	h.SetLayoutDefault([]byte("version: 1\nid: main\napplications:\n  - id: app\n    title: App\n    menus:\n      - id: home\n        title: Home\n        action: {type: window, windowKey: home}\n"))
	request := httptest.NewRequest("GET", "/v1/workspace/layout", nil)
	recorder := httptest.NewRecorder()
	h.handleLayout()(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("default status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response layoutResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Layout == nil || len(response.Layout.Applications) != 1 || response.Layout.Applications[0].ID != "app" {
		t.Fatalf("default response=%+v", response)
	}
	if err := os.MkdirAll(filepath.Join(root, "ui"), 0700); err != nil {
		t.Fatal(err)
	}
	invalid := []byte("version: 1\nid: main\napplications:\n  - id: app\n    title: App\n    menus:\n      - id: secret\n        title: Secret\n        hiddenWhen: {source: authorization, field: principal.features, equal: SECRET}\n        action: {type: window, windowKey: secret}\n")
	if err := os.WriteFile(filepath.Join(root, "ui", "layout.yaml"), invalid, 0600); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	h.handleLayout()(recorder, request)
	if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "equal") {
		t.Fatalf("override status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRemoteWindowDirectLoadRequiresApplicationGate(t *testing.T) {
	previous := ws.Root()
	ws.SetRoot(t.TempDir())
	defer ws.SetRoot(previous)
	h := NewMetadataHandler(nil, nil, "test")
	h.SetLayoutDefault([]byte(`version: 1
id: main
windowProviders:
  - {id: operations-ui, type: mcp, serverRef: operations, catalogTool: list_ui_windows, windowTool: get_ui_window}
applications:
  - id: operations
    title: Operations
    authorization: {resourceType: application}
    visibleWhen: {source: authorization, field: principal.features, contains: OPERATIONS_UI}
    windowCatalog: {provider: operations-ui}
`))
	restore := permittedview.SetDefaultRuntime(&permittedview.Runtime{Resolver: layoutResolver{features: []string{"OPERATIONS_UI"}}})
	if err := h.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"); err != nil {
		t.Fatalf("expected access: %v", err)
	}
	restore()
	restore = permittedview.SetDefaultRuntime(&permittedview.Runtime{Resolver: layoutResolver{}})
	defer restore()
	if err := h.AuthorizeRemoteWindow(context.Background(), "operations-ui", "overview"); err != policy.ErrDenied {
		t.Fatalf("expected application denial, got %v", err)
	}
}

func TestLayoutConditionRolesAndFeatures(t *testing.T) {
	condition := map[string]any{"all": []any{
		map[string]any{"source": "authorization", "field": "principal.roles", "contains": "operator"},
		map[string]any{"source": "authorization", "field": "principal.features", "contains": "OPERATIONS_UI"},
	}}
	if err := validateCondition(condition); err != nil {
		t.Fatal(err)
	}
	allowed := &permittedview.Snapshot{Principal: map[string]any{"roles": []string{"operator"}, "features": []string{"OPERATIONS_UI"}}}
	if !evalSnapshotCondition(condition, allowed) {
		t.Fatal("expected role and feature to allow")
	}
	denied := &permittedview.Snapshot{Principal: map[string]any{"roles": []string{"operator"}}}
	if evalSnapshotCondition(condition, denied) {
		t.Fatal("missing feature must deny")
	}
	capability := map[string]any{"source": "authorization", "field": "globalCapabilities.manageTools", "equals": true}
	if !evalSnapshotCondition(capability, &permittedview.Snapshot{GlobalCapabilities: map[string]bool{"manageTools": true}}) {
		t.Fatal("global capability condition should read typed snapshot map")
	}
}

func TestLayoutRejectsUnsafeActionParameters(t *testing.T) {
	layout := &Layout{Version: 1, ID: "main", Applications: []LayoutApplication{{ID: "app", Title: "App", Menus: []LayoutMenu{{ID: "record", Title: "Record", Action: &LayoutAction{Type: "window", WindowKey: "record", Parameters: map[string]any{"id": int64(9007199254740992)}}}}}}}
	if err := validateLayout(layout); err == nil || !strings.Contains(err.Error(), "safe range") {
		t.Fatalf("expected unsafe parameter rejection, got %v", err)
	}
}

func TestLayoutBatchesWindowDiscoveryPolicy(t *testing.T) {
	resolver := &layoutPolicyResolver{}
	restore := policy.SetDefaultRuntime(policy.NewRuntime(resolver, policy.OperationWindowView))
	defer restore()
	layout := &Layout{Version: 1, ID: "main", Applications: []LayoutApplication{{ID: "app", Title: "App", Menus: []LayoutMenu{
		{ID: "one", Title: "One", Action: &LayoutAction{Type: "window", WindowKey: "one"}},
		{ID: "two", Title: "Two", Action: &LayoutAction{Type: "window", WindowKey: "two"}},
	}}}}
	result, err := filterLayout(httptest.NewRequest("GET", "/v1/workspace/layout", nil), layout)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || resolver.request.ConversationID != "" || len(resolver.request.Candidates) != 2 {
		t.Fatalf("unexpected discovery request: calls=%d request=%+v", resolver.calls, resolver.request)
	}
	if len(result.Applications) != 1 || len(result.Applications[0].Menus) != 1 || result.Applications[0].Menus[0].ID != "one" {
		t.Fatalf("unexpected filtered layout: %+v", result)
	}
}

func TestLayoutTopbarActionRespectsWindowPolicy(t *testing.T) {
	resolver := &layoutPolicyResolver{}
	restore := policy.SetDefaultRuntime(policy.NewRuntime(resolver, policy.OperationWindowView))
	defer restore()
	layout := &Layout{Version: 1, ID: "main", Topbar: &LayoutTopbar{Actions: []LayoutMenu{{ID: "automation", Title: "Automation", Action: &LayoutAction{Type: "window", WindowKey: "schedule"}}}}}
	result, err := filterLayout(httptest.NewRequest("GET", "/v1/workspace/layout", nil), layout)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || len(resolver.request.Candidates) != 1 || resolver.request.Candidates[0].ID != "schedule" {
		t.Fatalf("unexpected policy request: %+v", resolver.request)
	}
	if result.Topbar == nil || len(result.Topbar.Actions) != 0 {
		t.Fatalf("denied topbar action remained visible: %+v", result.Topbar)
	}
}
