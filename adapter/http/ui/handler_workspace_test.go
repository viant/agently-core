package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/agently-core/workspace"
)

func TestWindowHandler_MergesWorkspaceForgeAssets(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(metaRoot, "window", "chat", "new", "main.yaml"), `
namespace: Chat
dialogs:
  - id: settings
    title: Settings
dataSource:
  meta: {}
view:
  content:
    containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "dialogs", "workItemPicker.yaml"), `
id: workItemPicker
title: Select Ad Order
dataSourceRef: work_item_lookup
content:
  id: workItemPickerContent
  dataSourceRef: work_item_lookup
  containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "work_item_lookup.yaml"), `
cardinality: collection
parameters: []
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/chat/new")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Dialogs []struct {
				Id string `json:"id"`
			} `json:"dialogs"`
			DataSource map[string]map[string]any `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}

	dialogs := map[string]bool{}
	for _, dialog := range payload.Data.Dialogs {
		dialogs[dialog.Id] = true
	}
	if !dialogs["settings"] || !dialogs["workItemPicker"] {
		t.Fatalf("expected built-in and workspace dialogs, got %#v", payload.Data.Dialogs)
	}
	if _, ok := payload.Data.DataSource["meta"]; !ok {
		t.Fatalf("expected built-in meta datasource")
	}
	if _, ok := payload.Data.DataSource["work_item_lookup"]; !ok {
		t.Fatalf("expected merged workspace datasource")
	}
}

func TestWindowHandler_LoadsWorkspaceOwnedForgeWindowWhenStaticWindowIsAbsent(t *testing.T) {
	metaRoot := t.TempDir()
	workspaceRoot := t.TempDir()
	prevRoot := workspace.Root()
	workspace.SetRoot(workspaceRoot)
	t.Cleanup(func() {
		workspace.SetRoot(prevRoot)
	})

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "windows", "order.yaml"), `
id: order
title: Order Summary
windowKey: order
namespace: Order Summary
view:
  content:
    id: orderRoot
    containers:
      - id: summary
        title: Summary
        items:
          - id: selectedSpend
            label: Spend
            type: label
            scope: metrics
            dataSourceRef: order_performance_period_today
            dataField: periodSummary.spend
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "order_performance_period_today.yaml"), `
id: order_performance_period_today
cardinality: collection
parameters:
  - name: order_id
    in: windowForm
    location: AdOrderId.0
`)

	server := httptest.NewServer(newHandler("file://"+metaRoot, nil))
	defer server.Close()

	resp, err := http.Get(server.URL + "/window/order")
	if err != nil {
		t.Fatalf("window request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Namespace string `json:"namespace"`
			View      struct {
				Content struct {
					ID string `json:"id"`
				} `json:"content"`
			} `json:"view"`
			DataSource map[string]map[string]interface{} `json:"dataSource"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Status != "ok" {
		t.Fatalf("expected ok status, got %q", payload.Status)
	}
	if payload.Data.Namespace != "Order Summary" {
		t.Fatalf("expected workspace namespace, got %q", payload.Data.Namespace)
	}
	if payload.Data.View.Content.ID != "orderRoot" {
		t.Fatalf("expected workspace content id, got %#v", payload.Data.View.Content.ID)
	}
	if _, ok := payload.Data.DataSource["order_performance_period_today"]; !ok {
		t.Fatalf("expected merged workspace datasource")
	}
}

func mustWriteWorkspaceUIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}
