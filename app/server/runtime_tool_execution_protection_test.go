package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/viant/agently-core/workspace"
)

func TestBuildWorkspaceRuntimeRejectsInvalidToolExecutionProtectionBeforeBootstrap(t *testing.T) {
	originalRoot := workspace.Root()
	t.Cleanup(func() { workspace.SetRoot(originalRoot) })
	root := t.TempDir()
	config := []byte(`default:
  toolExecutionProtection:
    enabled: true
    rules:
      - id: invalid
        tool: delivery/*
        mode: atMostOnce
`)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{WorkspaceRoot: root}); err == nil {
		t.Fatal("BuildWorkspaceRuntime() invalid protection error = nil")
	}
}
