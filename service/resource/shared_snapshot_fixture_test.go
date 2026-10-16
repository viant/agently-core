package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/viant/forge/backend/types"
)

func configuredSnapshotDefinition() NativeSnapshotDefinition {
	return NativeSnapshotDefinition{URI: "window://studio/overview", Title: "Overview", FormatVersion: 2, File: "window.json", Load: func(ctx context.Context, reader *ExtensionReader, file string) (json.RawMessage, error) {
		raw, err := reader.ReadFile(ctx, file)
		if err != nil {
			return nil, err
		}
		var window types.Window
		if err := json.Unmarshal(raw, &window); err != nil {
			return nil, err
		}
		imported, err := reader.ReadFile(ctx, "shared/title.txt")
		if err != nil {
			return nil, err
		}
		window.View.Content.Title += string(imported)
		variant := types.WindowResourceVariant{Window: &window, DataSources: map[string]json.RawMessage{}}
		fingerprint, err := types.WindowVariantFingerprint(variant)
		if err != nil {
			return nil, err
		}
		return json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	}}
}

func configuredSnapshotRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "window.json"), []byte(`{"view":{"content":{"id":"original","title":"Original"}}}`), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(root, "shared"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "shared/title.txt"), []byte("one"), 0600))
	return root
}
