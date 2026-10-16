package resource

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	identity "github.com/viant/forge/backend/resource"
)

func TestReportWindowMetadataPrepassLeavesMissingImportForSnapshot(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "extension/forge/windows")
	require.NoError(t, os.MkdirAll(directory, 0700))
	file := filepath.Join(directory, "broken.yaml")
	require.NoError(t, os.WriteFile(file, []byte("windowKey: broken\nview:\n  content: $import(mutation-sources.yaml)\n"), 0600))
	windows := []NativeSnapshotDefinition{{URI: "window://workspace/broken", File: "extension/forge/windows/broken.yaml"}}
	_, _, err := NativeWorkspaceReportDefinitions(context.Background(), root, "workspace", "", windows)
	require.NoError(t, err)
	require.Len(t, windows, 1, "prepass must leave the failed window for owning snapshot diagnostics")
	require.NoError(t, os.WriteFile(file, []byte("windowKey: broken\nview:\n  content: $import(../../../../outside.yaml)\n"), 0600))
	_, _, err = NativeWorkspaceReportDefinitions(context.Background(), root, "workspace", "", windows)
	require.ErrorIs(t, err, identity.ErrResourceDenied, "confinement errors must not be isolated by the metadata prepass")
	require.NoError(t, os.WriteFile(file, []byte("view: [invalid"), 0600))
	_, _, err = NativeWorkspaceReportDefinitions(context.Background(), root, "workspace", "", windows)
	require.Error(t, err, "malformed YAML retains strict behavior")
}
