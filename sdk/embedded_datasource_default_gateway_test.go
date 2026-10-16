package sdk

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/sdk/api"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/store/fs"
)

func TestDefaultSDKDatasourceUsesSamePublicSnapshotGateway(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	root := t.TempDir()
	old, oldRuntime := workspace.Root(), workspace.RuntimeRoot()
	workspace.SetRoot(t.TempDir())
	workspace.SetRuntimeRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old); workspace.SetRuntimeRoot(oldRuntime) })
	dir := filepath.Join(root, workspace.KindForgeDataSource)
	require.NoError(t, os.MkdirAll(dir, 0700))
	original := []byte("id: original-authored-id\nbackend:\n  kind: inline\n  rows:\n    - id: original-row\n")
	file := filepath.Join(dir, "original.yaml")
	require.NoError(t, os.WriteFile(file, original, 0600))
	rt, err := executor.NewBuilder().WithStore(fs.New(root)).WithAgentFinder(&streamTestAgentFinder{agent: &agentmdl.Agent{Identity: agentmdl.Identity{ID: "owned"}}}).WithModelFinder(&streamTestModelFinder{model: &streamTestModel{}}).Build(context.Background())
	require.NoError(t, err)
	defer rt.Close(context.Background())
	client, err := newBackendFromRuntime(rt)
	require.NoError(t, err)
	require.NotNil(t, rt.PrimitiveProviders)
	require.Same(t, rt.PrimitiveProviders, client.primitiveProviders)
	user := feedActorContext("normal-user")
	names, err := client.InlineReportWorkspaceDataSources(user)
	require.NoError(t, err)
	require.Contains(t, names, "original-authored-id")
	require.Contains(t, names, "datasource://workspace/original")
	got, err := client.FetchDatasource(user, &api.FetchDatasourceInput{ID: "original-authored-id"})
	require.NoError(t, err)
	require.Len(t, got.Rows, 1)
	require.Equal(t, "original-row", got.Rows[0]["id"])
	require.Empty(t, client.datasourceStore.List(), "default snapshot path does not publish YAML into a shared SDK store")
	require.NoError(t, client.InvalidateDatasourceCache(user, &api.InvalidateDatasourceCacheInput{ID: "datasource://workspace/original"}))
	current, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, original, current)
}
