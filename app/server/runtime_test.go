package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildWorkspaceRuntime_LoadsWorkspaceDefaults(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "embedders"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `default:
  agent: coder
  model: openai_gpt-5.4
  embedder: openai_text
  skills:
    model: openai_gpt-5.4
`
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	rt, _, _, err := BuildWorkspaceRuntime(context.Background(), RuntimeOptions{WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("BuildWorkspaceRuntime() error: %v", err)
	}
	if rt == nil || rt.Defaults == nil {
		t.Fatal("expected runtime defaults")
	}
	if rt.Defaults.Model != "openai_gpt-5.4" {
		t.Fatalf("defaults model = %q, want openai_gpt-5.4", rt.Defaults.Model)
	}
	if rt.Defaults.Agent != "coder" {
		t.Fatalf("defaults agent = %q, want coder", rt.Defaults.Agent)
	}
	if rt.Defaults.Embedder != "openai_text" {
		t.Fatalf("defaults embedder = %q, want openai_text", rt.Defaults.Embedder)
	}
	if rt.Defaults.Skills.Model != "openai_gpt-5.4" {
		t.Fatalf("defaults skills.model = %q, want openai_gpt-5.4", rt.Defaults.Skills.Model)
	}
}
