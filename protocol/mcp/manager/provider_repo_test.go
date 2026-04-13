package manager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRepoProviderOptions_ExpandsTransportURLTemplates(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", workspaceRoot)
	t.Setenv("STEWARD_MCP_URL", "https://override.example.com/mcp")

	mcpDir := filepath.Join(workspaceRoot, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("failed to create mcp dir: %v", err)
	}
	config := []byte(`name: steward
transport:
  type: streamable
  url: ${STEWARD_MCP_URL:-http://localhost:5002/mcp}
auth:
  backendForFrontend: true
  useIdToken: true
`)
	if err := os.WriteFile(filepath.Join(mcpDir, "steward.yaml"), config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	provider := NewRepoProvider()
	cfg, err := provider.Options(context.Background(), "steward")
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	if got, want := cfg.ClientOptions.Transport.URL, "https://override.example.com/mcp"; got != want {
		t.Fatalf("Transport.URL = %q, want %q", got, want)
	}
}

func TestRepoProviderOptions_UsesTemplateDefaultWhenEnvUnset(t *testing.T) {
	workspaceRoot := t.TempDir()
	t.Setenv("AGENTLY_WORKSPACE", workspaceRoot)

	mcpDir := filepath.Join(workspaceRoot, "mcp")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("failed to create mcp dir: %v", err)
	}
	config := []byte(`name: steward
transport:
  type: streamable
  url: ${STEWARD_MCP_URL:-http://localhost:5002/mcp}
`)
	if err := os.WriteFile(filepath.Join(mcpDir, "steward.yaml"), config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	provider := NewRepoProvider()
	cfg, err := provider.Options(context.Background(), "steward")
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	if got, want := cfg.ClientOptions.Transport.URL, "http://localhost:5002/mcp"; got != want {
		t.Fatalf("Transport.URL = %q, want %q", got, want)
	}
}
