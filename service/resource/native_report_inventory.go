package resource

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	view "github.com/viant/agently-core/protocol/ui/view"
	reportcatalog "github.com/viant/agently-core/service/reporting/catalog"
	"github.com/viant/forge/backend/reporting/registry"
	identity "github.com/viant/forge/backend/resource"
	"gopkg.in/yaml.v3"
)

// NativeWorkspaceReportDefinitions preserves authored report identities and
// binds builder dependencies to their original owning workspace window.
func NativeWorkspaceReportDefinitions(ctx context.Context, root, namespace, reportingRoot string, windows []NativeSnapshotDefinition) ([]NativeSnapshotDefinition, registry.Options, error) {
	options := registry.Options{WorkspaceRoot: root, ReportingRoot: reportingRoot}
	builderWindows := map[string]string{}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, options, err
	}
	defer opened.Close()
	reader := NewExtensionReader(opened)
	defer reader.Close()
	for i, window := range windows {
		raw, err := reader.ReadFile(ctx, window.File)
		if err != nil {
			return nil, options, err
		}
		var node yaml.Node
		if err = yaml.Unmarshal(raw, &node); err != nil {
			return nil, options, err
		}
		if err = resolveNativeAuthoredImports(ctx, reader, window.File, &node); err != nil {
			// This pass extracts optional report ownership metadata. Keep a window
			// with a missing import for the snapshot compiler to diagnose in isolation.
			if errors.Is(err, fs.ErrNotExist) && !errors.Is(err, identity.ErrResourceDenied) && !errors.Is(err, identity.ErrResourceStale) {
				continue
			}
			return nil, options, err
		}
		var spec view.Spec
		if err = node.Decode(&spec); err != nil {
			return nil, options, err
		}
		if spec.Title != "" {
			windows[i].Title = spec.Title
		}
		if spec.ReportBuilderRef != "" {
			if prior := builderWindows[spec.ReportBuilderRef]; prior != "" && prior != spec.WindowKey {
				return nil, options, ErrCollision
			}
			builderWindows[spec.ReportBuilderRef] = spec.WindowKey
		}
	}
	source, err := reportcatalog.NewReportResourceSource(reportcatalog.ReportResourceOptions{Options: options, DefaultNamespace: namespace, BuilderWindows: builderWindows})
	if err != nil {
		return nil, options, err
	}
	entries, err := source.List(ctx)
	if err != nil {
		return nil, options, err
	}
	current, err := registry.Discover(ctx, options)
	if err != nil {
		return nil, options, err
	}
	defs := make([]NativeSnapshotDefinition, 0, len(entries))
	for _, entry := range entries {
		// Owning-user documents require an explicit verified host authority adapter.
		// The unverified public metadata scope cannot establish private ownership.
		if entry.OwnerID != "" {
			continue
		}
		var file string
		for _, asset := range current.Reports {
			if asset.ResourceURI == entry.URI || asset.ID == entry.Name {
				file = asset.SourcePath
				break
			}
		}
		if file == "" {
			return nil, options, fs.ErrNotExist
		}
		if filepath.IsAbs(file) {
			file, err = filepath.Rel(root, file)
			if err != nil {
				return nil, options, err
			}
		}
		defs = append(defs, NativeSnapshotDefinition{URI: entry.URI, Title: entry.Title, FormatVersion: 1, File: filepath.ToSlash(file), Load: NativeReportLoader(entry.URI, reportcatalog.ReportResourceOptions{Options: options, DefaultNamespace: namespace, BuilderWindows: builderWindows}, nil)})
	}
	return defs, options, nil
}
