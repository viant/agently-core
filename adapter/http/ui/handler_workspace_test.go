package ui

import (
	"encoding/json"
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

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "dialogs", "adOrderPicker.yaml"), `
id: adOrderPicker
title: Select Ad Order
dataSourceRef: ad_order_lookup
content:
  id: adOrderPickerContent
  dataSourceRef: ad_order_lookup
  containers: []
`)

	mustWriteWorkspaceUIFile(t, filepath.Join(workspaceRoot, "extension", "forge", "datasources", "ad_order_lookup.yaml"), `
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
	if !dialogs["settings"] || !dialogs["adOrderPicker"] {
		t.Fatalf("expected built-in and workspace dialogs, got %#v", payload.Data.Dialogs)
	}
	if _, ok := payload.Data.DataSource["meta"]; !ok {
		t.Fatalf("expected built-in meta datasource")
	}
	if _, ok := payload.Data.DataSource["ad_order_lookup"]; !ok {
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
