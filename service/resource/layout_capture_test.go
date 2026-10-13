package resource

import (
	"context"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeLayoutCaptureAuthoringPrecedenceAndEmbeddedCopy(t *testing.T) {
	root := t.TempDir()
	fallback := []byte("version: 1\nid: embedded\napplications: [{id: embedded, menus: [{id: one, action: {type: window, windowKey: original}}]}]\n")
	got, err := CaptureNativeLayoutSource(context.Background(), root, fallback)
	require.NoError(t, err)
	require.Equal(t, "embedded", got.Source)
	fallback[0] = 'X'
	require.NoError(t, got.Check(context.Background()))
	require.Equal(t, byte('v'), got.Raw[0], "trusted startup bytes are copied")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "ui"), 0700))
	file := filepath.Join(root, "ui/layout.yaml")
	authored := []byte("version: 1\nid: authored\napplications: [{id: authored, menus: [{id: two, action: {type: window, windowKey: authored}}]}]\n")
	require.NoError(t, os.WriteFile(file, authored, 0600))
	require.NoError(t, got.Check(context.Background()), "new authored files cannot replace captured embedded bytes without explicit recapture")
	current, err := CaptureNativeLayoutSource(context.Background(), root, got.Raw)
	require.NoError(t, err)
	require.Equal(t, "ui/layout.yaml", current.Source)
	require.Equal(t, authored, current.Raw)
	require.NoError(t, os.WriteFile(file, append(authored, []byte("# changed\n")...), 0600))
	require.NoError(t, current.Check(context.Background()))
	require.Equal(t, authored, current.Raw, "captured layout remains unchanged after edits")
}
