package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	internalAuth "github.com/viant/agently-core/internal/auth"
	policy "github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
	forgeTypes "github.com/viant/forge/backend/types"
)

type fixedAuthorizationResolver struct{}

type waitingAuthorizationResolver struct{}
type unavailableAuthorizationResolver struct{}

func (unavailableAuthorizationResolver) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return nil, permittedview.ErrUnavailable
}

func (waitingAuthorizationResolver) Resolve(ctx context.Context, _ *permittedview.Request) (*permittedview.Snapshot, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (fixedAuthorizationResolver) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return &permittedview.Snapshot{
		AuthorizationVersion: "v1", ExpiresAt: time.Now().Add(time.Minute),
		Resources: map[string]*permittedview.Resource{
			"85141": {Type: "advertiser", ID: 85141, Capabilities: map[string]bool{"read": true, "write": false}},
		},
	}, nil
}

type windowPolicyResolver struct{}

type allowRequestedWindowResolver struct{}

func (allowRequestedWindowResolver) Resolve(_ context.Context, request *policy.Request) (*policy.Decision, error) {
	return &policy.Decision{PolicyVersion: "injected", ExpiresAt: time.Now().Add(time.Minute), Allow: true, AllowedIDs: []string{request.Candidates[0].ID}}, nil
}

func (windowPolicyResolver) Resolve(_ context.Context, request *policy.Request) (*policy.Decision, error) {
	return &policy.Decision{
		PolicyVersion: "v1", ExpiresAt: time.Now().Add(time.Minute), Allow: true,
		AllowedIDs: []string{"allowed"},
	}, nil
}

func TestWindowHandlerAppliesWholeWindowPolicyAndKeepsLegacyPermissionOptional(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "allowed.yaml"), "view: {content: {id: root}}\n")
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "denied.yaml"), "view: {content: {id: root}}\n")
	cleanup := policy.SetDefaultRuntime(policy.NewRuntime(windowPolicyResolver{}, policy.OperationWindowView))
	t.Cleanup(cleanup)

	allowed := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/window/allowed", nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d: %s", allowed.Code, allowed.Body.String())
	}
	denied := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/window/denied", nil))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("denied status = %d: %s", denied.Code, denied.Body.String())
	}
}

func TestInjectedWindowPolicyOverridesProcessDefault(t *testing.T) {
	metaRoot, workspaceRoot := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "protected.yaml"), "view: {content: {id: root}}\n")
	cleanup := policy.SetDefaultRuntime(policy.NewRuntime(windowPolicyResolver{}, policy.OperationWindowView))
	t.Cleanup(cleanup)
	injected := policy.NewRuntime(allowRequestedWindowResolver{}, policy.OperationWindowView)
	response := httptest.NewRecorder()
	newHandlerWithAuthorization("file://"+metaRoot, nil, injected, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/protected", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("injected policy was not used: status=%d body=%s", response.Code, response.Body.String())
	}
	isolated := httptest.NewRecorder()
	newHandlerWithAuthorization("file://"+metaRoot, nil, nil, nil).ServeHTTP(isolated, httptest.NewRequest(http.MethodGet, "/window/protected", nil))
	if isolated.Code != http.StatusOK {
		t.Fatalf("injected nil policy inherited process default: %d %s", isolated.Code, isolated.Body.String())
	}
}

func TestWindowHandlerBoundsPermissionPreflightWithoutGrantingAccess(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "advertiserList.yaml"), `
authorization:
  scope: principal
  resourceType: advertiser
  requestedGlobalCapabilities: [create]
view: {content: {id: root}}
`)
	cleanup := permittedview.SetDefaultRuntime(permittedview.NewRuntime(waitingAuthorizationResolver{}))
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/window/advertiserList?applyPermission=true", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 for unavailable permission service, got %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestWindowHandlerReportsPermissionAuthorityOutage(t *testing.T) {
	metaRoot, workspaceRoot := t.TempDir(), t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "protected.yaml"), "authorization: {scope: principal, resourceType: document}\nview: {content: {id: root}}\n")
	response := httptest.NewRecorder()
	newHandlerWithAuthorization("file://"+metaRoot, nil, nil, permittedview.NewRuntime(unavailableAuthorizationResolver{})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/window/protected?applyPermission=true", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "permission service unavailable\n" {
		t.Fatalf("outage status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestWindowHandlerRejectsAuthorizationSchemaMismatch(t *testing.T) {
	cleanup := permittedview.SetDefaultRuntime(permittedview.NewRuntime(nil))
	t.Cleanup(cleanup)
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "protected.yaml"), "authorization: {scope: principal, resourceType: document}\nview: {content: {id: root}}\n")
	request := httptest.NewRequest(http.MethodGet, "/window/protected?applyPermission=true&authorizationSchemaVersion=2", nil)
	response := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("schema mismatch status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFilterNavigationItemsRemovesDeniedWindowsAndEmptyGroups(t *testing.T) {
	items := []forgeTypes.NavigationItem{
		{ID: "group", ChildNodes: []forgeTypes.NavigationItem{{ID: "allowed", WindowKey: "allowed"}, {ID: "denied", WindowKey: "denied"}}},
		{ID: "empty", ChildNodes: []forgeTypes.NavigationItem{{ID: "hidden", WindowKey: "hidden"}}},
	}
	got := filterNavigationItems(items, map[string]bool{"allowed": true})
	if len(got) != 1 || got[0].ID != "group" || len(got[0].ChildNodes) != 1 || got[0].ChildNodes[0].WindowKey != "allowed" {
		t.Fatalf("filterNavigationItems() = %#v", got)
	}
}

func TestAuthzNavigationKeepsOpaqueWindowIDsExact(t *testing.T) {
	items := []forgeTypes.NavigationItem{{ID: "one", WindowKey: "Window-A"}, {ID: "two", WindowKey: "window-a"}}
	candidates := navigationWindowCandidatesWithMode(items, true)
	if len(candidates) != 2 {
		t.Fatalf("collapsed distinct IDs: %+v", candidates)
	}
	filtered := filterNavigationItemsWithMode(items, map[string]bool{"Window-A": true}, true)
	if len(filtered) != 1 || filtered[0].WindowKey != "Window-A" {
		t.Fatalf("case-widened grant: %+v", filtered)
	}
}

func TestWindowHandlerCompilesPermittedViewBeforeResponse(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "advertiser.yaml"), `
authorization:
  scope: resource
  resource:
    type: advertiser
    id: {source: windowform, selector: advertiserId}
  requestedCapabilities: [read, write]
resources:
  dataSources: [identity, edit]
view:
  content:
    id: root
    containers:
      - {id: overview, dataSourceRef: identity}
      - id: edit
        dataSourceRef: edit
        visibleWhen: {source: authorization, field: resource.capabilities.write, equals: true}
`)
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "identity.yaml"), "cardinality: collection\n")
	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "edit.yaml"), "cardinality: collection\n")

	cleanup := permittedview.SetDefaultRuntime(permittedview.NewRuntime(fixedAuthorizationResolver{}))
	t.Cleanup(cleanup)

	query := url.Values{}
	query.Set("applyPermission", "true")
	query.Set("windowParams", `{"advertiserId":85141}`)
	req := httptest.NewRequest(http.MethodGet, "/window/advertiser?"+query.Encode(), nil)
	req = req.WithContext(internalAuth.WithUserInfo(req.Context(), &internalAuth.UserInfo{Subject: "user-1"}))
	recorder := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			View struct {
				Content struct {
					Containers []struct {
						ID string `json:"id"`
					} `json:"containers"`
				} `json:"content"`
			} `json:"view"`
			DataSource map[string]any `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.View.Content.Containers) != 1 || payload.Data.View.Content.Containers[0].ID != "overview" {
		t.Fatalf("protected metadata was not pruned: %#v", payload.Data.View.Content.Containers)
	}
	if _, ok := payload.Data.DataSource["edit"]; ok {
		t.Fatalf("pruned datasource remained in response: %#v", payload.Data.DataSource)
	}
}

func TestWindowHandlerReturnsAuthoredMetadataUntilPermissionIsApplied(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() { workspace.SetRoot(previous) })

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "advertiser.yaml"), `
authorization:
  scope: resource
view:
  content:
    id: root
    containers:
      - {id: overview, dataSourceRef: identity}
      - id: edit
        dataSourceRef: edit
        visibleWhen: {source: authorization, field: resource.capabilities.write, equals: true}
`)

	req := httptest.NewRequest(http.MethodGet, "/window/advertiser", nil)
	recorder := httptest.NewRecorder()
	newHandler("file://"+metaRoot, nil).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected authored metadata response, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			View struct {
				Content struct {
					Containers []struct {
						ID string `json:"id"`
					} `json:"containers"`
				} `json:"content"`
			} `json:"view"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.View.Content.Containers) != 2 {
		t.Fatalf("authored metadata was pruned before applyPermission: %#v", payload.Data.View.Content.Containers)
	}
}
