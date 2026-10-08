package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/viant/agently-core/sdk"
	"github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/authz"
	forgetypes "github.com/viant/forge/backend/types"
)

type uiAuthzFacts struct{ facts authz.Facts }

func (p *uiAuthzFacts) Resolve(context.Context) (authz.Facts, error) { return p.facts, nil }

type uiRejectedFacts struct{}

func (uiRejectedFacts) Resolve(context.Context) (authz.Facts, error) {
	return authz.Facts{}, authz.ErrDenied
}

type uiAuthzStore map[authz.Resource]authz.Document

func (s uiAuthzStore) Get(_ context.Context, r authz.Resource) (authz.Document, error) {
	value, ok := s[r]
	if !ok {
		return authz.Document{}, authz.ErrDenied
	}
	return value, nil
}
func (uiAuthzStore) Replace(context.Context, authz.Document, int64, string) (authz.Document, error) {
	return authz.Document{}, authz.ErrDenied
}

func TestFileWindowAuthzAdmissionAndCapabilityPreflight(t *testing.T) {
	metaRoot, workspaceRoot := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "protected.yaml"), `
authorization:
  scope: resource
  resource: {type: customer, id: {source: windowForm, selector: CustomerID}}
  requestedCapabilities: [read]
view: {content: {id: root}}
`)
	facts := &uiAuthzFacts{authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, Exposures: []string{"FEATURE"}, EntityGroups: authz.EntityGroups{"customer": {"42"}}, ValidUntil: time.Now().Add(time.Hour)}}
	windowResource := authz.Resource{Kind: "window", ID: "protected", Version: "1", Tenant: "tenant"}
	entityResource := authz.Resource{Kind: "customer", ID: "catalog", Version: "1", Tenant: "tenant"}
	rule := &authz.Rule{Kind: "role", Value: "reader"}
	service := &authz.Service{Provider: facts, Store: uiAuthzStore{
		windowResource: {Resource: windowResource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: rule}}},
		entityResource: {Resource: entityResource, Revision: 1, Policies: map[string]authz.Policy{"retrieve": {Mode: "protected", Rule: rule, EntityType: "customer"}}},
	}}
	account := func(context.Context, authz.Facts) (string, error) { return "account-1", nil }
	gate := func(_ context.Context, used authz.Facts, _ string, _ authz.Resource, _ string, _ *authz.Entity) (policy.GateResult, error) {
		allowed := false
		for _, exposure := range used.Exposures {
			allowed = allowed || exposure == "FEATURE"
		}
		return policy.GateResult{Allow: allowed, Revision: "requirements-1", ValidUntil: time.Now().Add(time.Minute)}, nil
	}
	windowPolicy := policy.NewRuntime(&policy.AuthzResolver{Service: service, PolicyVersion: "policy-1", Account: account, Gate: gate, Resource: func(_ context.Context, _ string, candidate policy.Candidate) (authz.Resource, string, error) {
		return windowResource, "execute", nil
	}}, policy.OperationWindowView)
	windowPolicy.ExactIDs = true
	cleanupPolicy := policy.SetDefaultRuntime(windowPolicy)
	t.Cleanup(cleanupPolicy)
	capabilities := &permittedview.AuthzResolver{Service: service, Version: "mapping-1", Account: account, Gate: gate, Map: func(_ context.Context, _ string, id int, capability string, global bool) (authz.Resource, string, *authz.Entity, error) {
		if id != 42 || capability != "read" || global {
			return authz.Resource{}, "", nil, authz.ErrDenied
		}
		return entityResource, "retrieve", &authz.Entity{Type: "customer", ID: strconv.Itoa(id)}, nil
	}, EntityPermission: func(_ context.Context, _ authz.Facts, entity authz.Entity, capability string) (bool, error) {
		return entity.ID == "42" && capability == "read", nil
	}}
	cleanupView := permittedview.SetDefaultRuntime(permittedview.NewRuntime(capabilities))
	t.Cleanup(cleanupView)
	handler := newHandler("file://"+metaRoot, nil)
	open := func(path string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}
	permitted := open("/window/protected?applyPermission=true&windowParams=" + url.QueryEscape(`{"CustomerID":42}`))
	if permitted.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", permitted.Code, permitted.Body.String())
	}
	var payload struct {
		Authorization permittedview.Snapshot `json:"authorization"`
	}
	if err := json.Unmarshal(permitted.Body.Bytes(), &payload); err != nil || payload.Authorization.Resources["42"] == nil || !payload.Authorization.Resources["42"].Capabilities["read"] {
		t.Fatalf("missing verified capability: %+v %v", payload, err)
	}
	facts.facts.Exposures = nil
	for _, path := range []string{"/window/protected", "/window/protected?applyPermission=true&windowParams=" + url.QueryEscape(`{"CustomerID":42}`)} {
		denied := open(path)
		if denied.Code != http.StatusNotFound {
			t.Fatalf("gate bypassed on %q: status=%d body=%s", path, denied.Code, denied.Body.String())
		}
	}
}

func TestWholeWindowIdentityRejectionIsNotHiddenAsMissingNavigation(t *testing.T) {
	root := t.TempDir()
	mustWriteWorkspaceUIFile(t, filepath.Join(root, "shared", "navigation.yaml"), "- {id: orders, label: Orders, windowKey: orders}\n")
	resource := authz.Resource{Kind: "window", ID: "orders", Version: "1", Tenant: "tenant"}
	resolver := &policy.AuthzResolver{Service: &authz.Service{Provider: uiRejectedFacts{}}, PolicyVersion: "v1",
		Account: func(context.Context, authz.Facts) (string, error) { return "account", nil },
		Resource: func(context.Context, string, policy.Candidate) (authz.Resource, string, error) {
			return resource, "execute", nil
		},
		Gate: func(context.Context, authz.Facts, string, authz.Resource, string, *authz.Entity) (policy.GateResult, error) {
			t.Fatal("gate called with rejected identity")
			return policy.GateResult{}, nil
		},
	}
	admission := policy.NewRuntime(resolver, policy.OperationWindowView)
	handler := newHandlerWithAuthorization("file://"+root, nil, admission, nil)
	for _, path := range []string{"/window/orders", "/navigation"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("identity rejection on %s became hidden resource: status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestSDKV2StringIDPassesActualWindowPreflight(t *testing.T) {
	if !reflect.ValueOf(&forgetypes.AuthorizationSpec{}).Elem().FieldByName("SchemaVersion").IsValid() {
		t.Skip("pinned Forge predates v2 authorization metadata")
	}
	metaRoot, workspaceRoot := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "customer.yaml"), `
authorization:
  schemaVersion: 2
  scope: resource
  resource: {type: customer, id: {source: windowForm, selector: CustomerID}}
  requestedCapabilities: [read]
view: {content: {id: root}}
`)
	id := "9007199254740993"
	facts := &uiAuthzFacts{authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, EntityGroups: authz.EntityGroups{"customer": {authz.EntityID(id)}}, ValidUntil: time.Now().Add(time.Hour)}}
	windowResource := authz.Resource{Kind: "window", ID: "customer", Version: "1", Tenant: "tenant"}
	entityResource := authz.Resource{Kind: "customer", ID: "catalog", Version: "1", Tenant: "tenant"}
	rule := &authz.Rule{Kind: "role", Value: "reader"}
	service := &authz.Service{Provider: facts, Store: uiAuthzStore{
		windowResource: {Resource: windowResource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: rule}}},
		entityResource: {Resource: entityResource, Revision: 1, Policies: map[string]authz.Policy{"retrieve": {Mode: "protected", Rule: rule, EntityType: "customer"}}},
	}}
	account := func(context.Context, authz.Facts) (string, error) { return "account", nil }
	gate := func(_ context.Context, _ authz.Facts, _ string, _ authz.Resource, _ string, _ *authz.Entity) (policy.GateResult, error) {
		return policy.GateResult{Allow: true, Revision: "gate-v2", ValidUntil: time.Now().Add(time.Minute)}, nil
	}
	admission := policy.NewRuntime(&policy.AuthzResolver{Service: service, PolicyVersion: "v2", Account: account, Gate: gate, Resource: func(context.Context, string, policy.Candidate) (authz.Resource, string, error) {
		return windowResource, "execute", nil
	}}, policy.OperationWindowView)
	admission.ExactIDs = true
	capabilities := permittedview.NewRuntime(&permittedview.AuthzResolver{Service: service, Version: "v2", Account: account, Gate: gate, MapV2: func(_ context.Context, _, selectedID, capability string, global bool) (authz.Resource, string, *authz.Entity, error) {
		if global || capability != "read" {
			return authz.Resource{}, "", nil, authz.ErrDenied
		}
		return entityResource, "retrieve", &authz.Entity{Type: "customer", ID: selectedID}, nil
	}, EntityPermission: func(_ context.Context, _ authz.Facts, entity authz.Entity, capability string) (bool, error) {
		return entity.ID == id && capability == "read", nil
	}})
	handler := newHandlerWithAuthorization("file://"+metaRoot, nil, admission, capabilities)
	server := httptest.NewServer(http.StripPrefix("/v1/api/agently/forge", handler))
	defer server.Close()
	client, err := sdk.NewHTTP(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.ApplyPermission(context.Background(), "customer", &sdk.ApplyPermissionInput{AuthorizationSchemaVersion: 2, WindowParams: map[string]interface{}{"CustomerID": id}})
	if err != nil {
		t.Fatalf("v2 SDK preflight: %v", err)
	}
	var permitted struct {
		AuthorizationSnapshot struct {
			SchemaVersion int `json:"schemaVersion"`
			Resources     map[string]struct {
				ID           string          `json:"id"`
				Capabilities map[string]bool `json:"capabilities"`
			} `json:"resources"`
		} `json:"authorizationSnapshot"`
	}
	if err := json.Unmarshal(raw, &permitted); err != nil || permitted.AuthorizationSnapshot.SchemaVersion != 2 || permitted.AuthorizationSnapshot.Resources[id].ID != id || !permitted.AuthorizationSnapshot.Resources[id].Capabilities["read"] {
		t.Fatalf("v2 response lost exact ID: %s %v", raw, err)
	}
	if _, err := client.ApplyPermission(context.Background(), "customer", &sdk.ApplyPermissionInput{AuthorizationSchemaVersion: 2, WindowParams: map[string]interface{}{"CustomerID": float64(9007199254740993)}}); err == nil {
		t.Fatal("rounded numeric v2 ID passed server preflight")
	}
}

func TestFileAndBuiltInWindowsShareWholeWindowAdmission(t *testing.T) {
	metaRoot, workspaceRoot := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "file.yaml"), "view: {content: {id: root}}\n")
	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "builtin.yaml"), "view: {content: {id: root}}\n")
	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "shared", "navigation.yaml"), "- {id: file, label: File, windowKey: file, windowTitle: File}\n- {id: builtin, label: Built-in, windowKey: builtin, windowTitle: Built-in}\n")
	facts := &uiAuthzFacts{authz.Facts{Subject: "alice", Issuer: "issuer", Tenant: "tenant", Roles: []string{"reader"}, Exposures: []string{"FEATURE"}, ValidUntil: time.Now().Add(time.Hour)}}
	store := uiAuthzStore{}
	for _, id := range []string{"file", "builtin"} {
		resource := authz.Resource{Kind: "window", ID: id, Version: "1", Tenant: "tenant"}
		store[resource] = authz.Document{Resource: resource, Revision: 1, Policies: map[string]authz.Policy{"execute": {Mode: "protected", Rule: &authz.Rule{Kind: "role", Value: "reader"}}}}
	}
	service := &authz.Service{Store: store, Provider: facts}
	admission := policy.NewRuntime(&policy.AuthzResolver{Service: service, PolicyVersion: "v1", Account: func(context.Context, authz.Facts) (string, error) { return "account", nil }, Resource: func(_ context.Context, _ string, candidate policy.Candidate) (authz.Resource, string, error) {
		return authz.Resource{Kind: "window", ID: candidate.ID, Version: "1", Tenant: "tenant"}, "execute", nil
	}, Gate: func(_ context.Context, used authz.Facts, _ string, _ authz.Resource, _ string, _ *authz.Entity) (policy.GateResult, error) {
		return policy.GateResult{Allow: len(used.Exposures) == 1, Revision: "r1", ValidUntil: time.Now().Add(time.Minute)}, nil
	}}, policy.OperationWindowView)
	handler := newHandlerWithAuthorization("file://"+metaRoot, nil, admission, nil)
	for _, expectedStatus := range []int{http.StatusOK, http.StatusNotFound} {
		navigation := httptest.NewRecorder()
		handler.ServeHTTP(navigation, httptest.NewRequest(http.MethodGet, "/navigation", nil))
		var listing struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(navigation.Body.Bytes(), &listing); err != nil {
			t.Fatal(err)
		}
		wantCount := 2
		if expectedStatus == http.StatusNotFound {
			wantCount = 0
		}
		if navigation.Code != http.StatusOK || len(listing.Data) != wantCount {
			t.Fatalf("navigation status=%d items=%d want=%d body=%s", navigation.Code, len(listing.Data), wantCount, navigation.Body.String())
		}
		for _, id := range []string{"file", "builtin"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/"+id, nil))
			if response.Code != expectedStatus {
				t.Fatalf("%s status=%d want=%d body=%s", id, response.Code, expectedStatus, response.Body.String())
			}
		}
		facts.facts.Exposures = nil
	}
}
