package augmenter

import (
	"context"
	"path/filepath"
	"testing"
)

func TestResolveResourceDBPathUsesRuntimeRootWithoutGlobalIndexOverride(t *testing.T) {
	t.Setenv("AGENTLY_RUNTIME_ROOT", filepath.Join(t.TempDir(), "runtime"))
	t.Setenv("AGENTLY_INDEX_PATH", filepath.Join(t.TempDir(), "user-index"))

	got := ResolveResourceDBPath(context.Background(), "${runtimeRoot}/index/shared/product.sqlite")
	want := filepath.Join(getenv("AGENTLY_RUNTIME_ROOT"), "index", "shared", "product.sqlite")
	if got != want {
		t.Fatalf("ResolveResourceDBPath() = %q, want %q", got, want)
	}
}
