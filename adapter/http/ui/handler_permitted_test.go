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
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/agently-core/workspace"
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
