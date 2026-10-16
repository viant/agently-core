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
	config := []byte(`name: analyst
transport:
  type: streamable
  url: ${STEWARD_MCP_URL:-http://localhost:5002/mcp}
auth:
  backendForFrontend: true
  useIdToken: true
`)
	if err := os.WriteFile(filepath.Join(mcpDir, "analyst.yaml"), config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	provider := NewRepoProvider()
	cfg, err := provider.Options(context.Background(), "analyst")
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
	config := []byte(`name: analyst
transport:
  type: streamable
  url: ${STEWARD_MCP_URL:-http://localhost:5002/mcp}
`)
	if err := os.WriteFile(filepath.Join(mcpDir, "analyst.yaml"), config, 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	provider := NewRepoProvider()
	cfg, err := provider.Options(context.Background(), "analyst")
	if err != nil {
		t.Fatalf("Options() error = %v", err)
	}
	if got, want := cfg.ClientOptions.Transport.URL, "http://localhost:5002/mcp"; got != want {
		t.Fatalf("Transport.URL = %q, want %q", got, want)
	}
}

func TestRepoProviderExplicitPublicCatalogPreservesAnonymousAuth(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, auth string
		public                 bool
	}{
		{name: "anonymous-public", visibility: "public", public: true},
		{name: "authenticated-public", visibility: "public", auth: "auth:\n  backendForFrontend: true\n"},
		{name: "private", visibility: "private"},
		{name: "unspecified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AGENTLY_WORKSPACE", root)
			dir := filepath.Join(root, "mcp")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			definition := "name: fixture\ntransport:\n  type: streamable\n  url: http://fixture.test/mcp\n" + tc.auth
			if tc.visibility != "" {
				definition += "toolsListVisibility: " + tc.visibility + "\n"
			}
			if err := os.WriteFile(filepath.Join(dir, "fixture.yaml"), []byte(definition), 0644); err != nil {
				t.Fatal(err)
			}
			provider := NewRepoProvider()
			options, err := provider.Options(context.Background(), "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if (options.Auth == nil) != tc.public {
				t.Fatalf("auth nil = %v, want %v", options.Auth == nil, tc.public)
			}
			owner, err := New(provider)
			if err != nil {
				t.Fatal(err)
			}
			if got := owner.IsToolsListPublic(context.Background(), "fixture"); got != tc.public {
				t.Fatalf("public = %v, want %v", got, tc.public)
			}
		})
	}
}
