package workspace

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspacePathsConcurrentLazyInitializationAndConfiguration(t *testing.T) {
	resetWorkspaceStateForTest(t)
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv(envKey, root)
	t.Setenv("AGENTLY_RUNTIME_ROOT", "")
	t.Setenv("AGENTLY_STATE_PATH", "")
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				require.Equal(t, root, Root())
				require.Equal(t, root, RuntimeRoot())
				require.Equal(t, filepath.Join(root, "state"), StateRoot())
				require.Equal(t, filepath.Join(root, "agents"), ResolvePathTemplate("${workspaceRoot}/agents"))
				SetRoot(root)
				SetRuntimeRoot(root)
				SetStateRoot(filepath.Join(root, "state"))
			}
		}()
	}
	close(start)
	workers.Wait()
	// The old setters' invalidation and environment precedence remain intact.
	next := filepath.Join(t.TempDir(), "next")
	SetRoot(next)
	require.Equal(t, next, Root())
	require.Equal(t, next, RuntimeRoot())
	require.Equal(t, filepath.Join(next, "state"), StateRoot())
}

func TestWorkspaceBootstrapCanResolvePaths(t *testing.T) {
	resetWorkspaceStateForTest(t)
	root := filepath.Join(t.TempDir(), "workspace")
	t.Setenv(envKey, root)
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "")
	var called bool
	SetBootstrapHook(func(*BootstrapStore) error {
		called = true
		require.Equal(t, root, Root())
		require.Equal(t, filepath.Join(root, "agents"), ResolvePathTemplate("${workspaceRoot}/agents"))
		return nil
	})
	require.Equal(t, root, Root())
	require.True(t, called, "bootstrap must run outside the path cache lock")
}
