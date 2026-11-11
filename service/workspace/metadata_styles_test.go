package workspace

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/protocol/mcp/manager"
	resources "github.com/viant/agently-core/service/resource"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/ui/style"
)

func TestWorkspaceThemeMetadataAndProtectedRoutes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, style.Directory)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 1
defaultTheme: branded
themes:
  - id: branded
    label: Branded
    fallbackMode: light
    modes: {light: {}}
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	h := NewMetadataHandler(nil, nil, "test")
	h.styles = style.New(func() string { return root })
	mux := http.NewServeMux()
	h.Register(mux)
	sessions := auth.NewManager(0, nil)
	sessions.Put(nil, &auth.Session{ID: "theme-session", Username: "user", Subject: "user"})
	protected := auth.Protect(&auth.Config{Enabled: true, Local: &auth.Local{Enabled: true}, CookieName: "session"}, sessions)(mux)
	get := func(url string, authenticated bool, etag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: "session", Value: "theme-session"})
		}
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		return rec
	}
	first := get("/v1/workspace/metadata", true, "")
	if first.Code != 200 {
		t.Fatalf("metadata: %d %s", first.Code, first.Body)
	}
	var metadata MetadataResponse
	if err := json.Unmarshal(first.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.UIThemes == nil || metadata.UIStyles == nil || metadata.WorkspaceID == "" {
		t.Fatalf("missing style metadata: %s", first.Body)
	}
	if metadata.UIStyles.ThemeRevision != metadata.UIThemes.Revision {
		t.Fatal("catalog/CSS mismatch")
	}
	for _, url := range []string{"/v1/workspace/metadata", metadata.UIThemes.Href, metadata.UIStyles.Href} {
		if got := get(url, false, "").Code; got != 401 {
			t.Errorf("unauthenticated %s: %d", url, got)
		}
		permitted := get(url, true, "")
		if permitted.Code != 200 {
			t.Errorf("authenticated %s: %d", url, permitted.Code)
		}
		if url != "/v1/workspace/metadata" {
			if got := get(url, false, permitted.Header().Get("ETag")).Code; got != 401 {
				t.Errorf("cached asset bypasses authentication: %d", got)
			}
			if got := get(url, true, permitted.Header().Get("ETag")).Code; got != 304 {
				t.Errorf("authenticated cache revalidation: %d", got)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest+"files: [custom.css]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom.css"), []byte(`.agently-workspace {color:red}`), 0600); err != nil {
		t.Fatal(err)
	}
	var after MetadataResponse
	if err := json.Unmarshal(get("/v1/workspace/metadata", true, "").Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after.MetadataVersion == metadata.MetadataVersion || after.UIStyles.Revision == metadata.UIStyles.Revision {
		t.Fatal("style edit did not invalidate metadata")
	}
	if after.WorkspaceID != metadata.WorkspaceID {
		t.Fatal("style edit changed workspace identity")
	}
}

func TestManagedStyleOwnershipSuppressesLocalAssetsBeforeFirstLayoutRead(t *testing.T) {
	h := NewMetadataHandler(nil, nil, "test")
	pool, poolErr := manager.New(&layoutPublishedTrap{})
	if poolErr != nil {
		t.Fatal(poolErr)
	}
	t.Cleanup(func() { pool.CloseConversation("") })
	actor, verify := resources.PublicReadActors()
	gateway := resources.NewGateway(pool, actor, verify, "host")
	t.Cleanup(gateway.Close)
	if err := gateway.RegisterPublishedMetadataSource(resources.Connection{Name: "public-catalog", ProviderIdentity: "public-owner"}, []string{"global"}, &publishedStyleFixture{absent: true}); err != nil {
		t.Fatal(err)
	}
	h.SetStarterTaskSources(gateway, nil)
	err := h.SetCapturedLayout(t.TempDir(), "embedded", []byte("version: 1\nid: shell\napplications: []\n"), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// A central-style startup must never invoke the local compiler, even before
	// layout/global metadata arrives. Its custom asset routes remain unmounted.
	h.styles = nil
	mux := http.NewServeMux()
	h.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata: %d %s", rec.Code, rec.Body.String())
	}
	var metadata MetadataResponse
	if err = json.Unmarshal(rec.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.UIStyleSource != "managed" || metadata.UIStyles != nil || metadata.UIThemes != nil {
		t.Fatalf("local styling leaked into managed startup: %+v", metadata)
	}
	for _, path := range []string{"/v1/workspace/ui/styles/old.css", "/v1/workspace/ui/themes/old.json", "/v1/workspace/ui/fonts/old.woff2"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("local custom asset remained mounted: %s %d", path, rec.Code)
		}
	}
}
