package binding

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/afs"
	"github.com/viant/afs/file"
)

func testdataURI(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("resolve testdata: %v", err)
	}
	return file.Scheme + "://" + abs
}

// TestResolveTextImports_Basic expands two sibling imports.
func TestResolveTextImports_Basic(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()

	main := testdataURI(t, "imports/basic/main.tmpl")
	data, err := fs.DownloadWithURL(ctx, main)
	if err != nil {
		t.Fatalf("download main: %v", err)
	}
	out, err := ResolveTextImports(ctx, fs, string(data), main)
	if err != nil {
		t.Fatalf("ResolveTextImports: %v", err)
	}
	for _, want := range []string{"Header line.", "PERSONA_BODY", "CONTRACT_BODY", "Trailer line."} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "$import(") {
		t.Errorf("unexpanded $import remains in output:\n%s", out)
	}
}

// TestResolveTextImports_Nested expands recursively (outer includes inner).
func TestResolveTextImports_Nested(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()

	main := testdataURI(t, "imports/nested/main.tmpl")
	data, err := fs.DownloadWithURL(ctx, main)
	if err != nil {
		t.Fatalf("download main: %v", err)
	}
	out, err := ResolveTextImports(ctx, fs, string(data), main)
	if err != nil {
		t.Fatalf("ResolveTextImports: %v", err)
	}
	for _, want := range []string{"OUTER_START", "INNER_BODY", "OUTER_END"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

// TestResolveTextImports_Cycle rejects A → B → A chains.
func TestResolveTextImports_Cycle(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()

	main := testdataURI(t, "imports/cycle/a.md")
	data, err := fs.DownloadWithURL(ctx, main)
	if err != nil {
		t.Fatalf("download a.md: %v", err)
	}
	_, err = ResolveTextImports(ctx, fs, string(data), main)
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") && !strings.Contains(err.Error(), "depth") {
		t.Errorf("expected cycle/depth error, got: %v", err)
	}
}

// TestResolveTextImports_Escape preserves $$import(...) as literal $import(...).
func TestResolveTextImports_Escape(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()

	main := testdataURI(t, "imports/escape/main.tmpl")
	data, err := fs.DownloadWithURL(ctx, main)
	if err != nil {
		t.Fatalf("download main: %v", err)
	}
	out, err := ResolveTextImports(ctx, fs, string(data), main)
	if err != nil {
		t.Fatalf("ResolveTextImports: %v", err)
	}
	// The $$ escape must resolve to a literal $import( in the output.
	if !strings.Contains(out, "$import(literal.md)") {
		t.Errorf("expected literal $import(literal.md) in output (from $$import escape), got:\n%s", out)
	}
	// The non-escaped one must have been expanded.
	if !strings.Contains(out, "REAL_BODY") {
		t.Errorf("expected REAL_BODY (from real $import) in output, got:\n%s", out)
	}
	// And the non-escaped directive itself must be gone.
	if strings.Contains(out, "$import(real.md)") {
		t.Errorf("unexpanded $import(real.md) remains in output:\n%s", out)
	}
}

// TestResolveTextImports_NoDirective is a fast-path when the text carries no $import.
func TestResolveTextImports_NoDirective(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	plain := "no imports here\njust text\n"
	out, err := ResolveTextImports(ctx, fs, plain, testdataURI(t, "imports/basic/main.tmpl"))
	if err != nil {
		t.Fatalf("ResolveTextImports: %v", err)
	}
	if out != plain {
		t.Errorf("expected text unchanged, got:\n%s", out)
	}
}

// TestResolveTextImports_AbsoluteRejected rejects absolute paths.
func TestResolveTextImports_AbsoluteRejected(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	text := "$import(/etc/passwd)\n"
	_, err := ResolveTextImports(ctx, fs, text, testdataURI(t, "imports/basic/main.tmpl"))
	if err == nil {
		t.Fatalf("expected absolute-path rejection, got nil")
	}
	if !strings.Contains(err.Error(), "relative") {
		t.Errorf("expected message about relative path, got: %v", err)
	}
}

// TestResolveTextImports_InlineNotExpanded makes sure a directive that
// is not alone on a line is left untouched.
func TestResolveTextImports_InlineNotExpanded(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	text := "See $import(parts/persona.md) below.\n"
	out, err := ResolveTextImports(ctx, fs, text, testdataURI(t, "imports/basic/main.tmpl"))
	if err != nil {
		t.Fatalf("ResolveTextImports: %v", err)
	}
	if out != text {
		t.Errorf("inline $import should not expand, got:\n%s", out)
	}
}
