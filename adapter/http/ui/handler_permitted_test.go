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

func (fixedAuthorizationResolver) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return &permittedview.Snapshot{
		AuthorizationVersion: "v1", ExpiresAt: time.Now().Add(time.Minute),
		Resources: map[string]*permittedview.Resource{
			"85141": {Type: "advertiser", ID: 85141, Capabilities: map[string]bool{"read": true, "write": false}},
		},
	}, nil
}

type windowPolicyResolver struct{}

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
    id: {source: resource, selector: advertiserId}
  requestedCapabilities: [read, write]
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
	query.Set("resource", `{"advertiserId":85141}`)
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
