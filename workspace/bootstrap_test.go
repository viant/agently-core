package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
)

func TestIsEmptyWorkspaceAt(t *testing.T) {
	ctx := context.Background()
	fs := afs.New()
	tmp := t.TempDir()

	t.Run("missing root is empty", func(t *testing.T) {
		empty, err := IsEmptyWorkspaceAt(ctx, fs, filepath.Join(tmp, "missing"))
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("placeholder only is empty", func(t *testing.T) {
		root := filepath.Join(tmp, "placeholder_only")
		require.NoError(t, os.MkdirAll(filepath.Join(root, KindAgent), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, KindAgent, ".keep"), nil, 0o644))

		empty, err := IsEmptyWorkspaceAt(ctx, fs, root)
		require.NoError(t, err)
		require.True(t, empty)
	})

	t.Run("config file makes workspace non-empty", func(t *testing.T) {
		root := filepath.Join(tmp, "has_config")
		require.NoError(t, os.MkdirAll(root, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("models: []\n"), 0o644))

		empty, err := IsEmptyWorkspaceAt(ctx, fs, root)
		require.NoError(t, err)
		require.False(t, empty)
	})

	t.Run("nested yaml makes workspace non-empty", func(t *testing.T) {
		root := filepath.Join(tmp, "has_agent")
		require.NoError(t, os.MkdirAll(filepath.Join(root, KindAgent), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, KindAgent, "demo.yaml"), []byte("name: demo\n"), 0o644))

		empty, err := IsEmptyWorkspaceAt(ctx, fs, root)
		require.NoError(t, err)
		require.False(t, empty)
	})
}
