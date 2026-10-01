package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveChildPathRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if _, err := ResolveChildPath(root, "../outside.yaml"); err == nil {
		t.Fatal("parent traversal should fail")
	}
	if err := os.WriteFile(filepath.Join(outside, "layout.yaml"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "layout.yaml"), filepath.Join(root, "layout.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveChildPath(root, "layout.yaml"); err == nil {
		t.Fatal("symlink escape should fail")
	}
	if _, err := ResolveChildPath(root, "ui/layout.yaml"); err != nil {
		t.Fatalf("missing optional layout should remain eligible for fallback: %v", err)
	}
}
