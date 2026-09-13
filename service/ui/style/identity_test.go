package style

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlyWorkspaceKeepsStylesWithoutPersistentIdentity(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	root := t.TempDir()
	seed(t, root)
	if err := os.Chmod(root, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0755)
	p := New(func() string { return root }).Current(context.Background())
	if p.Styles == nil || p.Themes == nil {
		t.Fatalf("read-only identity failure disabled appearance: %+v", p)
	}
	if p.WorkspaceID != "" || len(p.Diagnostics) == 0 {
		t.Fatalf("expected session-only identity fallback: %+v", p)
	}
}
func TestConfiguredIdentityWorksOnReadOnlyWorkspace(t *testing.T) {
	root := t.TempDir()
	seed(t, root)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("workspaceId: stable-readonly"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(root, 0755)
	p := New(func() string { return root }).Current(context.Background())
	if p.WorkspaceID != "stable-readonly" || p.Styles == nil || len(p.Diagnostics) != 0 {
		t.Fatalf("configured identity failed: %+v", p)
	}
	if _, err := os.Stat(filepath.Join(root, ".workspace-id")); !os.IsNotExist(err) {
		t.Fatal("configured identity unexpectedly wrote a generated ID")
	}
}
