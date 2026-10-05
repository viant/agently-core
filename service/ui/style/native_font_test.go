package style

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/viant/agently-core/protocol/ui/theme"
	"golang.org/x/image/font/gofont/goregular"
)

func TestNativeFontCatalogAndAuthenticatedAssetRoute(t *testing.T) {
	root := t.TempDir()
	seed(t, root)
	write(t, root, "manifest.yaml", strings.Replace(manifest, "style: normal", "nativeFile: fonts/native.ttf\n        style: normal", 1))
	write(t, root, "fonts/native.ttf", string(goregular.TTF))
	svc := New(func() string { return root })
	p := svc.Current(context.Background())
	if p.Themes == nil || len(p.Diagnostics) != 0 {
		t.Fatalf("publication: %+v", p)
	}
	var catalog theme.Catalog
	if err := json.Unmarshal(request(svc, p.Themes.Href, "").Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Fonts) != 1 || len(catalog.Fonts[0].Faces) != 1 {
		t.Fatalf("fonts: %+v", catalog.Fonts)
	}
	face := catalog.Fonts[0].Faces[0]
	if face.Native == nil || face.Native.Format != "ttf" || face.Native.SizeBytes != len(goregular.TTF) || len(face.Native.SHA256) != 64 {
		t.Fatalf("native: %+v", face.Native)
	}
	served := request(svc, face.Native.Href, "")
	if served.Code != 200 || served.Body.String() != string(goregular.TTF) || served.Header().Get("Content-Type") != "font/ttf" || !strings.Contains(served.Header().Get("Cache-Control"), "private") {
		t.Fatalf("native response: %d %#v", served.Code, served.Header())
	}
	if request(svc, face.Native.Href, served.Header().Get("ETag")).Code != 304 {
		t.Fatal("missing native ETag")
	}
	if strings.Contains(request(svc, p.Styles.Href, "").Body.String(), ".ttf") {
		t.Fatal("native companion altered web CSS")
	}
	// Malformed native edits preserve the prior valid snapshot and fail closed.
	write(t, root, "fonts/native.ttf", "\x00\x01\x00\x00invalid")
	invalid := svc.Current(context.Background())
	if invalid.Styles.Revision != p.Styles.Revision || len(invalid.Diagnostics) == 0 {
		t.Fatalf("invalid native edit accepted: %+v", invalid)
	}
	// The same digest is inaccessible after switching workspace roots.
	root = t.TempDir()
	if request(svc, face.Native.Href, "").Code != 404 {
		t.Fatal("native bytes leaked across workspace switch")
	}
}

func TestNativeFontPathValidation(t *testing.T) {
	for _, name := range []string{"../native.ttf", "/native.ttf", "https://a/font.ttf", "fonts/f.woff2", "fonts/f.ttf?x", "fonts/f\\.ttf"} {
		if validNativeFontPath(name) {
			t.Fatalf("accepted %q", name)
		}
	}
	for _, name := range []string{"fonts/native.ttf", "fonts/native.otf"} {
		if !validNativeFontPath(name) {
			t.Fatalf("rejected %q", name)
		}
	}
}
