package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/workspace"
	mcpclient "github.com/viant/mcp/client"
)

func TestRegistryUsesManagedInventoryInsteadOfWorkspaceFiles(t *testing.T) {
	oldRoot := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(oldRoot) })
	t.Setenv("AGENTLY_MCP_SERVERS", "")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "mcp"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "mcp", "legacy.yaml"), []byte("name: legacy\ntransport:\n  type: streamable\n  url: https://fixture.test/mcp\n"), 0600))
	manager := &outputSchemaManager{}
	registry := newReconnectTestRegistry(&manager.scriptedReconnectManager)
	registry.mgr = manager
	registry.internal = map[string]mcpclient.Interface{"native": nil}
	names, err := registry.listServers(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"service", "native"}, names)
	require.Equal(t, 1, manager.namesCalls)
	require.NotContains(t, names, "legacy", "registry must not bypass dynamic inventory through filesystem discovery")
}

type unavailableInventoryManager struct{ outputSchemaManager }

func (*unavailableInventoryManager) Names(context.Context) ([]string, error) {
	return nil, errors.New("inventory unavailable")
}
func TestRegistryPreservesManagedInventoryErrors(t *testing.T) {
	manager := &unavailableInventoryManager{}
	registry := newReconnectTestRegistry(&manager.scriptedReconnectManager)
	registry.mgr = manager
	_, err := registry.listServers(context.Background())
	require.ErrorContains(t, err, "inventory unavailable")
}
