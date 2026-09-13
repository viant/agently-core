package style

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/ui/theme"
)

func write(t *testing.T, root, name, contents string) {
	t.Helper()
	file := filepath.Join(root, Directory, name)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}
func request(s *Service, url, etag string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	s.Register(mux)
	r := httptest.NewRequest(http.MethodGet, url, nil)
	if etag != "" {
		r.Header.Set("If-None-Match", etag)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

const manifest = `version: 1
files: [shared.css]
defaultTheme: branded
themes:
  - id: branded
    label: Branded
    fallbackMode: light
    tokens: {control.radius: 8}
    files: [brand.css]
    modes:
      light: {}
      dark:
        files: [dark.css]
`

func seed(t *testing.T, root string) {
	write(t, root, "manifest.yaml", manifest)
	write(t, root, "shared.css", `.agently-workspace .shared {color: red}`)
	write(t, root, "brand.css", `.agently-workspace[data-forge-theme="branded"] .brand {color: blue}`)
	write(t, root, "dark.css", `.agently-workspace[data-forge-theme="branded"][data-forge-color-mode="dark"] .dark {color: white}`)
}
func TestSnapshotLifecycle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := New(func() string { return root })
	if p := s.Current(ctx); p.Styles != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("missing manifest: %+v", p)
	}
	seed(t, root)
	first := s.Current(ctx)
	if first.Styles == nil || first.Themes == nil || first.WorkspaceID == "" || len(first.Diagnostics) != 0 {
		t.Fatalf("publication: %+v", first)
	}
	css := request(s, first.Styles.Href, "")
	if css.Code != 200 || !strings.Contains(css.Body.String(), "--forge-control-radius: 8px") {
		t.Fatalf("CSS: %d %s", css.Code, css.Body)
	}
	if css.Header().Get("Content-Type") != "text/css; charset=utf-8" || css.Header().Get("Cache-Control") != "private, no-cache" || css.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal(css.Header())
	}
	body := css.Body.String()
	if !(strings.Index(body, ".shared") < strings.Index(body, "--forge-control-radius") && strings.Index(body, "--forge-control-radius") < strings.Index(body, ".brand") && strings.Index(body, ".brand") < strings.Index(body, ".dark")) {
		t.Fatal("incorrect stylesheet order")
	}
	if w := request(s, first.Styles.Href, css.Header().Get("ETag")); w.Code != 304 {
		t.Fatalf("conditional response %d", w.Code)
	}
	var catalog theme.Catalog
	if err := json.Unmarshal(request(s, first.Themes.Href, "").Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if catalog.Themes[0].Modes["dark"]["control.radius"] != float64(8) {
		t.Fatal(catalog)
	}
	write(t, root, "shared.css", `.agently-workspace .shared {color: green}`)
	second := s.Current(ctx)
	if second.Styles.Revision == first.Styles.Revision || second.WorkspaceID != first.WorkspaceID {
		t.Fatal("CSS edit did not update revision independently of identity")
	}
	if got := request(s, first.Styles.Href, "").Body.String(); got != body {
		t.Fatal("old revision changed")
	}
	write(t, root, "dark.css", `@import "https://example.com/remote.css";`)
	invalid := s.Current(ctx)
	if invalid.Styles.Revision != second.Styles.Revision || len(invalid.Diagnostics) == 0 {
		t.Fatal("invalid replacement did not retain last valid snapshot")
	}
	other := t.TempDir()
	root = other
	if p := s.Current(ctx); p.Styles != nil {
		t.Fatal("workspace switch leaked old styles")
	}
	if w := request(s, first.Styles.Href, ""); w.Code != 404 {
		t.Fatal("workspace switch leaked cached revision")
	}
	seed(t, root)
	third := s.Current(ctx)
	if third.WorkspaceID == first.WorkspaceID {
		t.Fatal("different workspaces share preference identity")
	}
	if err := os.Remove(filepath.Join(root, Directory, "manifest.yaml")); err != nil {
		t.Fatal(err)
	}
	if p := s.Current(ctx); p.Styles != nil {
		t.Fatal("deletion retained styles")
	}
	if w := request(s, third.Styles.Href, ""); w.Code != 404 {
		t.Fatal("deletion retained asset")
	}
}
func TestInvalidAssets(t *testing.T) {
	for _, name := range []string{"../escape.css", "/tmp/x.css", "https://example.com/x.css", "nested/../x.css", "x.js", "x%2e.css", "x\\y.css"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "manifest.yaml", fmt.Sprintf("version: 1\nfiles: [%q]\n", name))
			if p := New(func() string { return root }).Current(context.Background()); p.Styles != nil || len(p.Diagnostics) == 0 {
				t.Fatalf("accepted %q", name)
			}
		})
	}
	for _, tc := range []struct{ name, manifest string }{
		{"duplicate", "version: 1\nfiles: [same.css, same.css]"},
		{"missing", "version: 1\nfiles: [missing.css]"},
		{"inherited duplicate", strings.Replace(manifest, "files: [brand.css]", "files: [shared.css]", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			seed(t, root)
			write(t, root, "same.css", `.agently-workspace {color:red}`)
			write(t, root, "manifest.yaml", tc.manifest)
			if p := New(func() string { return root }).Current(context.Background()); p.Styles != nil || len(p.Diagnostics) == 0 {
				t.Fatal("accepted invalid assets")
			}
		})
	}
	t.Run("symlink escape", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "outside.css")
		os.WriteFile(outside, []byte(`.agently-workspace {color:red}`), 0600)
		write(t, root, "manifest.yaml", "version: 1\nfiles: [escape.css]")
		if err := os.Symlink(outside, filepath.Join(root, Directory, "escape.css")); err != nil {
			t.Fatal(err)
		}
		p := New(func() string { return root }).Current(context.Background())
		if p.Styles != nil || len(p.Diagnostics) == 0 {
			t.Fatal("accepted escaping symlink")
		}
	})
	t.Run("size", func(t *testing.T) {
		root := t.TempDir()
		write(t, root, "manifest.yaml", "version: 1\nfiles: [big.css]")
		write(t, root, "big.css", strings.Repeat(" ", MaxCSSBytes+1))
		if p := New(func() string { return root }).Current(context.Background()); p.Styles != nil || len(p.Diagnostics) == 0 {
			t.Fatal("accepted oversized CSS")
		}
	})
}
func TestRestartAndCacheEviction(t *testing.T) {
	root := t.TempDir()
	seed(t, root)
	s := New(func() string { return root })
	p := s.Current(context.Background())
	fresh := New(func() string { return root })
	if w := request(fresh, p.Themes.Href, ""); w.Code != 200 {
		t.Fatal("could not reconstruct immutable revision after restart")
	}
	if got := fresh.Current(context.Background()).WorkspaceID; got != p.WorkspaceID {
		t.Fatal("identity changed after restart")
	}
	for i := 0; i < maxSnapshots; i++ {
		write(t, root, "shared.css", fmt.Sprintf(".agently-workspace {opacity: %g}", float64(i)/10))
		s.Current(context.Background())
	}
	if w := request(s, p.Styles.Href, ""); w.Code != 404 {
		t.Fatal("evicted revision must not return newer bytes")
	}
}
func TestCSSOnlyAndIdentityOverride(t *testing.T) {
	root := t.TempDir()
	write(t, root, "manifest.yaml", "version: 1\nfiles: [shared.css]")
	write(t, root, "shared.css", `.agently-workspace {color:red}`)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("workspaceId: moved-workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	p := New(func() string { return root }).Current(context.Background())
	if p.Themes != nil || p.Styles.ThemeRevision != "" || p.WorkspaceID != "moved-workspace" {
		t.Fatal(p)
	}
}

func TestWorkspaceOverridesLast(t *testing.T) {
	root := t.TempDir()
	seed(t, root)
	write(t, root, "manifest.yaml", manifest+"overrides: [overrides.css]\n")
	write(t, root, "overrides.css", `.agently-workspace .workspace-final {color: green}`)
	s := New(func() string { return root })
	p := s.Current(context.Background())
	if p.Styles == nil {
		t.Fatalf("%+v", p)
	}
	body := request(s, p.Styles.Href, "").Body.String()
	if strings.Index(body, ".workspace-final") <= strings.Index(body, ".dark") {
		t.Fatal("workspace overrides must follow theme mode CSS")
	}
}

func TestInvalidWorkspaceOverrideRetainsSnapshot(t *testing.T) {
	root := t.TempDir()
	seed(t, root)
	s := New(func() string { return root })
	before := s.Current(context.Background())
	if before.Styles == nil {
		t.Fatalf("%+v", before)
	}
	for _, files := range []string{"[missing.css]", "[shared.css]", "[../outside.css]", "[override.css, override.css]"} {
		write(t, root, "manifest.yaml", manifest+"overrides: "+files+"\n")
		after := s.Current(context.Background())
		if after.Styles == nil || after.Styles.Revision != before.Styles.Revision || len(after.Diagnostics) == 0 {
			t.Fatalf("invalid overrides %s must preserve working snapshot: %+v", files, after)
		}
	}
}
