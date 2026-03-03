package embedder

import (
	"context"
	"testing"

	"github.com/viant/agently-core/genai/embedder/provider"
)

func TestFinder_AddConfig_Find(t *testing.T) {
	f := New()
	cfg := &provider.Config{
		ID: "test-embedder",
		Options: provider.Options{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	}
	f.AddConfig("test-embedder", cfg)

	ids := f.Ids()
	found := false
	for _, id := range ids {
		if id == "test-embedder" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'test-embedder' in Ids(), got %v", ids)
	}
}

func TestFinder_Remove(t *testing.T) {
	f := New()
	cfg := &provider.Config{
		ID: "to-remove",
		Options: provider.Options{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	}
	f.AddConfig("to-remove", cfg)
	v1 := f.Version()

	f.Remove("to-remove")
	v2 := f.Version()

	if v2 <= v1 {
		t.Errorf("expected version to increment after Remove, got v1=%d v2=%d", v1, v2)
	}

	_, err := f.Find(context.Background(), "to-remove")
	if err == nil {
		t.Error("expected error after Remove, got nil")
	}
}

func TestFinder_Version(t *testing.T) {
	f := New()
	v0 := f.Version()

	f.AddConfig("a", &provider.Config{ID: "a", Options: provider.Options{Provider: "openai", Model: "m"}})
	v1 := f.Version()

	f.AddConfig("b", &provider.Config{ID: "b", Options: provider.Options{Provider: "openai", Model: "m"}})
	v2 := f.Version()

	if v1 <= v0 || v2 <= v1 {
		t.Errorf("version should increase monotonically: v0=%d v1=%d v2=%d", v0, v1, v2)
	}
}

func TestFinder_DropEmbedder(t *testing.T) {
	f := New()
	cfg := &provider.Config{
		ID: "drop-test",
		Options: provider.Options{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
	}
	f.AddConfig("drop-test", cfg)

	// DropEmbedder should keep config but remove cached instance.
	f.DropEmbedder("drop-test")

	// Config should still be there — Ids should list it.
	ids := f.Ids()
	found := false
	for _, id := range ids {
		if id == "drop-test" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected config to remain after DropEmbedder")
	}
}
