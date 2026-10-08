package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRawResourceBoundaryKindsPathsAndAliases(t *testing.T) {
	root := t.TempDir()
	guard := NewRawResourceBoundary(root)
	for _, relative := range []string{"windows/private.yaml", "extension/forge/windows/private.yaml", "extension/forge/reporting/presets/private.yaml", "intents/private.yaml", "skills/private/SKILL.md", "prompts/private.yaml"} {
		if err := guard.CheckURI(filepath.Join(root, relative), false); err == nil {
			t.Fatalf("protected path allowed: %s", relative)
		}
	}
	if err := guard.CheckURI(root, true); err == nil {
		t.Fatal("recursive parent scan allowed")
	}
	if err := guard.CheckURI(root, false); err != nil {
		t.Fatal("nonrecursive root browse blocked")
	}
	for _, relative := range []string{"agents/allowed.yaml", "models/allowed.yaml", "config/defaults.yaml", "templates/allowed.yaml"} {
		if err := guard.CheckURI(filepath.Join(root, relative), true); err != nil {
			t.Fatalf("unrelated resource denied: %s", relative)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "windows"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "windows"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := guard.CheckURI(filepath.Join(root, "alias", "new.yaml"), false); err == nil {
		t.Fatal("missing write under symlink escaped protection")
	}
	if err := guard.CheckResource("agents", "../windows/private"); err == nil {
		t.Fatal("relative escape accepted")
	}
	if err := guard.CheckResource("models", "provider:model"); err != nil {
		t.Fatal("valid model filename changed")
	}
}
