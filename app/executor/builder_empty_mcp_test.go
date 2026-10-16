package executor_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/workspace"
)

func TestBuilderBuildWithoutMCPDirectory(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	require.NoDirExists(t, filepath.Join(root, workspace.KindMCP))
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Close(context.Background())) })
	names, err := runtime.MCPManager.Names(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, names, "runtime-owned MCP sources remain registered without external definitions")
	_, err = runtime.MCPManager.Options(context.Background(), "unknown-server")
	require.Error(t, err, "minimal workspace must not fabricate unknown server configuration")
}
