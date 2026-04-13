package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedHandler_TargetAwareNavigationAndWindowResponses(t *testing.T) {
	rootDir := t.TempDir()
	root := "file://" + rootDir

	mustWriteUIFile(t, filepath.Join(rootDir, "shared", "navigation.yaml"), `
- id: shared
  label: Shared
  icon: home
  windowKey: shared
  windowTitle: Shared
`)
	mustWriteUIFile(t, filepath.Join(rootDir, "web", "navigation.yaml"), `
- id: web
  label: Web
  icon: globe
  windowKey: web
  windowTitle: Web
`)
	mustWriteUIFile(t, filepath.Join(rootDir, "window", "schedule", "shared", "main.yaml"), `
view:
  content:
    containers:
      - id: sharedSchedule
`)
	mustWriteUIFile(t, filepath.Join(rootDir, "window", "schedule", "web", "main.yaml"), `
view:
  content:
    containers:
      - id: webSchedule
`)

	server := httptest.NewServer(newHandler(root, nil))
	defer server.Close()

	t.Run("navigation uses target branch", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/navigation?platform=web&formFactor=desktop&surface=browser")
		if err != nil {
			t.Fatalf("navigation request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
		}

		var payload struct {
			Status string `json:"status"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode navigation response: %v", err)
		}
		if payload.Status != "ok" {
			t.Fatalf("expected status ok, got %q", payload.Status)
		}
		if len(payload.Data) != 1 || payload.Data[0].ID != "web" {
			t.Fatalf("expected web navigation item, got %#v", payload.Data)
		}
	})

	t.Run("window uses target branch", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/window/schedule?platform=web&formFactor=desktop&surface=browser")
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
				View struct {
					Content struct {
						Containers []struct {
							ID string `json:"id"`
						} `json:"containers"`
					} `json:"content"`
				} `json:"view"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode window response: %v", err)
		}
		if payload.Status != "ok" {
			t.Fatalf("expected status ok, got %q", payload.Status)
		}
		if len(payload.Data.View.Content.Containers) != 1 || payload.Data.View.Content.Containers[0].ID != "webSchedule" {
			t.Fatalf("expected web schedule container, got %#v", payload.Data.View.Content.Containers)
		}
	})
}

func mustWriteUIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}
