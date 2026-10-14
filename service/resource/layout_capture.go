package resource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/agently-core/workspace/config"
)

// CaptureNativeLayoutSource is trusted startup assembly, not a request loader.
// Explicit host payloads are selected by the caller first; an authored layout
// wins over the original embedded fallback. The selected generation is fixed.
func CaptureNativeLayoutSource(ctx context.Context, root string, fallback []byte) (*CapturedLayoutSource, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("layout capture requires trusted workspace root")
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	ref, explicit := "ui/layout.yaml", false
	if cfg != nil {
		if ui, ok := cfg.Raw["ui"].(map[string]any); ok {
			if layout, ok := ui["layout"].(map[string]any); ok {
				if path, ok := layout["ref"].(string); ok && strings.TrimSpace(path) != "" {
					ref = path
					explicit = true
				}
			}
		}
	}
	file, err := workspace.ResolveChildPath(root, ref)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, file)
	if err != nil {
		return nil, err
	}
	relative = filepath.ToSlash(relative)
	_, err = os.Stat(file)
	embedded := os.IsNotExist(err) && !explicit
	if err != nil && !embedded {
		return nil, err
	}
	if embedded && len(fallback) == 0 {
		return nil, nil
	}
	uri, _ := identity.ParseResourceURI("navigation://workspace/layout")
	result := &CapturedLayoutSource{Root: root, Source: relative}
	capture := func(raw []byte) error { result.Raw = append([]byte(nil), raw...); return nil }
	var def NativeSnapshotDefinition
	if embedded {
		result.Source = "embedded"
		def, err = SnapshotEmbeddedLayoutMenuDefinition(fallback, uri.String(), capture)
	} else {
		def, err = SnapshotLayoutMenuDefinition(relative, uri.String())
		def.Load = NativeLayoutMenuLoaderWithCapture(capture)
	}
	if err != nil {
		return nil, err
	}
	snapshot, err := NewNativeAssetSnapshot(ctx, root, []NativeSnapshotDefinition{def}, NativeSnapshotOptions{})
	if err != nil {
		return nil, err
	}
	result.Check = func(ctx context.Context) error {
		_, err := snapshot.Candidates(ctx, uri)
		return err
	}
	return result, nil
}
