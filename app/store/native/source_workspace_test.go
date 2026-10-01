package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	xmodule "github.com/viant/x/module"
)

func TestSourceWorkspaceDoesNotWalkUnrelatedSDKBuildTrees(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/core\n\ngo 1.25\n"), 0600))
	for _, dir := range []string{"internal/datly/goal/read", "internal/store/conversationtree", "sdk/android/build/volatile"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal/datly/goal/read/reader.go"), []byte("package read\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal/store/conversationtree/tree.go"), []byte("package conversationtree\n"), 0600))
	volatile := filepath.Join(root, "sdk/android/build/volatile")
	require.NoError(t, os.Chmod(volatile, 0000))
	t.Cleanup(func() { _ = os.Chmod(volatile, 0700) })
	workspace, err := sourceWorkspace(context.Background(), root)
	require.NoError(t, err)
	var found []string
	err = workspace.Walk(context.Background(), []string{"example.com/core/internal/datly/...", "example.com/core/internal/store/..."}, nil, func(file xmodule.File) error {
		found = append(found, file.ImportPath)
		return nil
	})
	require.NoError(t, err, "irrelevant build directories must be outside discovery")
	require.ElementsMatch(t, []string{"example.com/core/internal/datly/goal/read", "example.com/core/internal/store/conversationtree"}, found)
}
