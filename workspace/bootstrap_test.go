package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/viant/afs"
)

func TestEnsureDefaultAt_UsesHooks(t *testing.T) {
	ctx := context.Background()
	afsSvc := afs.New()

	t.Run("hook runs from EnsureDefaultAt", func(t *testing.T) {
		SetBootstrapHook(func(store *BootstrapStore) error {
			src := fstest.MapFS{
				"defaults/agents/custom.yaml": &fstest.MapFile{Data: []byte("name: custom\n")},
			}
			return store.SeedFromFS(ctx, src, "defaults")
		})
		t.Cleanup(func() { SetBootstrapHook(nil) })

		root := filepath.Join(t.TempDir(), "workspace")
		EnsureDefaultAt(ctx, afsSvc, root)

		data, err := os.ReadFile(filepath.Join(root, "agents", "custom.yaml"))
		require.NoError(t, err)
		require.Equal(t, "name: custom\n", string(data))

		_, err = os.Stat(filepath.Join(root, "config.yaml"))
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}

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

	t.Run("mcp only workspace is treated as empty for bootstrap", func(t *testing.T) {
		root := filepath.Join(tmp, "mcp_only")
		require.NoError(t, os.MkdirAll(filepath.Join(root, KindMCP, "system"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, KindMCP, "system", "os.yaml"), []byte("name: system/os\n"), 0o644))

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

func TestSeedFromFS(t *testing.T) {
	ctx := context.Background()
	afsSvc := afs.New()

	t.Run("seeds nested files", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "workspace")
		src := fstest.MapFS{
			"defaults/config.yaml":       &fstest.MapFile{Data: []byte("models: []\n")},
			"defaults/agents/demo.yaml":  &fstest.MapFile{Data: []byte("name: demo\n")},
			"defaults/workflows/.keep":   &fstest.MapFile{Data: []byte{}},
			"defaults/tools/sample.json": &fstest.MapFile{Data: []byte("{\"name\":\"sample\"}\n")},
		}
		require.NoError(t, SeedFromFS(ctx, afsSvc, root, src, "defaults"))

		data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
		require.NoError(t, err)
		require.Equal(t, "models: []\n", string(data))

		data, err = os.ReadFile(filepath.Join(root, "agents", "demo.yaml"))
		require.NoError(t, err)
		require.Equal(t, "name: demo\n", string(data))
	})

	t.Run("does not overwrite existing", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "workspace")
		require.NoError(t, os.MkdirAll(root, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("models: [existing]\n"), 0o644))
		src := fstest.MapFS{
			"defaults/config.yaml": &fstest.MapFile{Data: []byte("models: []\n")},
		}
		require.NoError(t, SeedFromFS(ctx, afsSvc, root, src, "defaults"))
		data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
		require.NoError(t, err)
		require.Equal(t, "models: [existing]\n", string(data))
	})

	t.Run("missing prefix no-op", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "workspace")
		src := fstest.MapFS{
			"defaults/config.yaml": &fstest.MapFile{Data: []byte("models: []\n")},
		}
		require.NoError(t, SeedFromFS(ctx, afsSvc, root, src, "missing"))
		_, err := os.Stat(filepath.Join(root, "config.yaml"))
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
}
