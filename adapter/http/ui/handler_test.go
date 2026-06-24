package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestTargetContextFromRequest_NormalizesRepeatedAndCommaCapabilities(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "repeated values",
			raw:  "/window/order?platform=android&formFactor=phone&surface=app&capabilities=markdown&capabilities=chart",
			want: []string{"markdown", "chart"},
		},
		{
			name: "comma packed legacy value",
			raw:  "/window/order?capabilities=markdown,chart",
			want: []string{"markdown", "chart"},
		},
		{
			name: "mixed repeated and comma values",
			raw:  "/window/order?capabilities=markdown,%20chart&capabilities=attachments&capabilities=voice,camera",
			want: []string{"markdown", "chart", "attachments", "voice", "camera"},
		},
		{
			name: "blank values omitted",
			raw:  "/window/order?capabilities=markdown,,%20&capabilities=chart",
			want: []string{"markdown", "chart"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.raw, nil)
			got := targetContextFromRequest(req)
			if got == nil {
				t.Fatalf("target context is nil")
			}
			if !reflect.DeepEqual(got.Capabilities, tt.want) {
				t.Fatalf("capabilities mismatch: got %#v want %#v", got.Capabilities, tt.want)
			}
		})
	}
}

func TestWindowHandler_NormalizesWindowKeyWithoutNetworkListener(t *testing.T) {
	rootDir := t.TempDir()
	root := "file://" + rootDir

	mustWriteUIFile(t, filepath.Join(rootDir, "window", "schedule", "shared", "main.yaml"), `
view:
  content:
    containers:
      - id: sharedSchedule
`)

	handler := newHandler(root, nil)
	req := httptest.NewRequest(http.MethodGet, "/window/%20schedule%20", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
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
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode window response: %v", err)
	}
	if payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", payload.Status)
	}
	if len(payload.Data.View.Content.Containers) != 1 || payload.Data.View.Content.Containers[0].ID != "sharedSchedule" {
		t.Fatalf("expected shared schedule container, got %#v", payload.Data.View.Content.Containers)
	}
}

func TestWindowHandler_RejectsBlankWindowKeyWithoutNetworkListener(t *testing.T) {
	handler := newHandler("file://"+t.TempDir(), nil)
	req := httptest.NewRequest(http.MethodGet, "/window/%20%20", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "window key is required") {
		t.Fatalf("expected window key error, got %q", rec.Body.String())
	}
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
