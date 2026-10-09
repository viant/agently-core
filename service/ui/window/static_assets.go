package window

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	wsmeta "github.com/viant/agently-core/workspace/service/meta"
)

// StaticAssetCompilation is owned by one serialized static source compilation.
// It contains parsed authored assets only, never actors, grants or result rows.
// The host must discard it at the end of the confined source generation.
type StaticAssetCompilation struct {
	mu         sync.Mutex
	root       string
	catalog    *workspaceCatalog
	hydrations uint64
}

func NewStaticAssetCompilation() *StaticAssetCompilation { return &StaticAssetCompilation{} }
func (s *StaticAssetCompilation) HydrationCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hydrations
}
func (s *StaticAssetCompilation) load(ctx context.Context, root string, svc *wsmeta.Service) (*workspaceCatalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalog != nil {
		if s.root != root {
			return nil, fmt.Errorf("static asset compilation cannot cross source roots")
		}
		return s.catalog, nil
	}
	catalog, err := loadWorkspaceCatalog(ctx, svc)
	if err != nil {
		return nil, err
	}
	s.catalog = catalog
	s.root = root
	s.hydrations++
	return catalog, nil
}
func cloneStaticValue[T any](value T) (T, error) {
	var clone T
	raw, err := json.Marshal(value)
	if err != nil {
		return clone, err
	}
	err = json.Unmarshal(raw, &clone)
	return clone, err
}
func cloneWorkspaceCatalog(source *workspaceCatalog) (*workspaceCatalog, error) {
	result := newWorkspaceCatalog()
	var err error
	if result.dataSources, err = cloneStaticValue(source.dataSources); err != nil {
		return nil, err
	}
	for key, value := range source.dataSourcePaths {
		result.dataSourcePaths[key] = value
	}
	for key, value := range source.dialogs {
		dialog, err := cloneStaticValue(value.dialog)
		if err != nil {
			return nil, err
		}
		result.dialogs[key] = workspaceDialogAsset{path: value.path, dialog: dialog}
	}
	for key, value := range source.registries {
		schemas, err := cloneStaticValue(value.Schemas)
		if err != nil {
			return nil, err
		}
		models, err := cloneStaticValue(value.ResourceModels)
		if err != nil {
			return nil, err
		}
		result.registries[key] = &modelRegistry{name: value.name, path: value.path, Schemas: schemas, ResourceModels: models}
	}
	for key, value := range source.schemas {
		schema, err := cloneStaticValue(value.schema)
		if err != nil {
			return nil, err
		}
		result.schemas[key] = catalogSchema{schema: schema, registry: value.registry}
	}
	for key, value := range source.models {
		model, err := cloneStaticValue(value.model)
		if err != nil {
			return nil, err
		}
		result.models[key] = catalogModel{model: model, registry: value.registry}
	}
	for key, value := range source.conflictedSchemas {
		result.conflictedSchemas[key] = value
	}
	for key, value := range source.conflictedModels {
		result.conflictedModels[key] = value
	}
	return result, nil
}
func (o LoaderOptions) catalog(ctx context.Context, root string, svc *wsmeta.Service) (*workspaceCatalog, error) {
	if o.StaticAssets == nil {
		return loadWorkspaceCatalog(ctx, svc)
	}
	parsed, err := o.StaticAssets.load(ctx, root, svc)
	if err != nil {
		return nil, err
	}
	return cloneWorkspaceCatalog(parsed)
}
