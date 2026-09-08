package window

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/viant/afs"
	metaURL "github.com/viant/afs/url"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

// LoadWorkspaceWindow loads a workspace-owned Forge window and merges the
// workspace-declared dialogs and datasources onto it so every consumer sees the
// same effective surface truth.
func LoadWorkspaceWindow(ctx context.Context, windowKey string, target *metaSvc.TargetContext) (*forgeTypes.Window, error) {
	windowKey = strings.TrimSpace(windowKey)
	if windowKey == "" {
		return nil, nil
	}
	workspaceWindowRoot := "file://" + filepath.ToSlash(filepath.Join(workspace.Root(), workspace.KindForgeWindow))
	loader := metaSvc.New(afs.New(), workspaceWindowRoot)
	if _, err := loader.ResolveWindowBase(ctx, metaURL.Join(workspaceWindowRoot, windowKey, "main"), target); err == nil {
		window, err := forgeHandlers.LoadWindowStructure(ctx, loader, workspaceWindowRoot, windowKey, "", target)
		if err != nil {
			return nil, err
		}
		if window == nil || window.View.Content == nil {
			return nil, fmt.Errorf("workspace forge window %q has no view content", windowKey)
		}
		if window.Actions == nil || strings.TrimSpace(window.Actions.Code) == "" {
			jsBase := metaURL.Join(workspaceWindowRoot, windowKey)
			if resolvedJSPath, err := loader.ResolveWindowAsset(ctx, jsBase, ".js", target); err == nil {
				code, err := loader.Download(ctx, resolvedJSPath)
				if err != nil {
					return nil, err
				}
				window.SetCode(code)
			}
		}
		if err := mergeWorkspaceWindowActionRefs(ctx, loader, workspaceWindowRoot, window, target); err != nil {
			return nil, err
		}
		if err := MergeWorkspaceForgeAssets(ctx, window); err != nil {
			return nil, err
		}
		return window, nil
	}

	svc := wsmeta.New(afs.New(), workspace.Root())
	windowPath := filepath.Join(workspace.KindForgeWindow, windowKey+".yaml")
	exists, err := svc.Exists(ctx, windowPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	window := &forgeTypes.Window{}
	if err := svc.Load(ctx, windowPath, window); err != nil {
		return nil, err
	}
	if window.View.Content == nil {
		return nil, nil
	}
	jsBase := metaURL.Join(workspaceWindowRoot, windowKey)
	if resolvedJSPath, err := loader.ResolveWindowAsset(ctx, jsBase, ".js", target); err == nil {
		code, err := loader.Download(ctx, resolvedJSPath)
		if err != nil {
			return nil, err
		}
		window.SetCode(code)
	}
	if err := mergeWorkspaceWindowActionRefs(ctx, loader, workspaceWindowRoot, window, target); err != nil {
		return nil, err
	}
	if err := MergeWorkspaceForgeAssets(ctx, window); err != nil {
		return nil, err
	}
	return window, nil
}

func mergeWorkspaceWindowActionRefs(ctx context.Context, loader *metaSvc.Service, workspaceWindowRoot string, window *forgeTypes.Window, target *metaSvc.TargetContext) error {
	actionRefs := workspaceWindowActionRefs(window)
	if len(actionRefs) == 0 {
		return nil
	}
	code := make([]string, 0, len(actionRefs)+1)
	if window.Actions != nil && strings.TrimSpace(window.Actions.Code) != "" {
		code = append(code, strings.TrimSpace(window.Actions.Code))
	}
	seen := map[string]bool{}
	for _, actionRef := range actionRefs {
		actionRef = strings.TrimSpace(actionRef)
		if actionRef == "" || seen[actionRef] {
			continue
		}
		seen[actionRef] = true
		jsBase := metaURL.Join(workspaceWindowRoot, actionRef)
		resolvedJSPath, err := loader.ResolveWindowAsset(ctx, jsBase, ".js", target)
		if err != nil {
			return fmt.Errorf("resolve workspace window action ref %q: %w", actionRef, err)
		}
		source, err := loader.Download(ctx, resolvedJSPath)
		if err != nil {
			return fmt.Errorf("load workspace window action ref %q: %w", actionRef, err)
		}
		code = append(code, strings.TrimSpace(string(source)))
	}
	if len(code) == 0 {
		return nil
	}
	window.SetCode([]byte("(() => Object.assign({},\n" + strings.Join(code, ",\n") + "\n))()"))
	return nil
}

func workspaceWindowActionRefs(window *forgeTypes.Window) []string {
	if window == nil {
		return nil
	}
	result := append([]string(nil), window.ActionRefs...)
	if window.View.Content == nil || window.View.Content.Dashboard == nil {
		return result
	}
	raw := window.View.Content.Dashboard.ReportBuilder["actionRefs"]
	switch actual := raw.(type) {
	case []string:
		result = append(result, actual...)
	case []interface{}:
		for _, item := range actual {
			if value := strings.TrimSpace(fmt.Sprint(item)); value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

// MergeWorkspaceForgeAssets merges workspace dialogs and datasources into the
// supplied window without overwriting window-owned declarations.
func MergeWorkspaceForgeAssets(ctx context.Context, window *forgeTypes.Window) error {
	if window == nil {
		return nil
	}
	svc := wsmeta.New(afs.New(), workspace.Root())

	dataSources, err := loadWorkspaceDataSources(ctx, svc)
	if err != nil {
		return err
	}
	if len(dataSources) > 0 {
		if window.DataSource == nil {
			window.DataSource = map[string]forgeTypes.DataSource{}
		}
		for id, dataSource := range dataSources {
			if _, exists := window.DataSource[id]; exists {
				continue
			}
			window.DataSource[id] = dataSource
		}
	}
	if err := mergeWorkspaceResourceModels(ctx, svc, window); err != nil {
		return err
	}
	dialogs, err := loadWorkspaceDialogs(ctx, svc)
	if err != nil {
		return err
	}
	if len(dialogs) > 0 {
		existing := map[string]bool{}
		for _, dialog := range window.Dialogs {
			id := strings.TrimSpace(dialog.Id)
			if id != "" {
				existing[id] = true
			}
		}
		for _, asset := range dialogs {
			dialog := asset.dialog
			id := strings.TrimSpace(dialog.Id)
			if id == "" || existing[id] {
				continue
			}
			probe := *window
			probe.View = forgeTypes.View{}
			probe.Dialogs = []forgeTypes.Dialog{dialog}
			if err := forgeTypes.ValidateResourceModels(&probe); err != nil {
				log.Printf("workspace forge: skipping invalid global dialog %s (%s): %v", id, workspaceAssetPath(asset.path), err)
				continue
			}
			window.Dialogs = append(window.Dialogs, dialog)
			existing[id] = true
		}
	}
	if err := enrichWorkspaceWindow(ctx, window); err != nil {
		return err
	}
	return forgeTypes.ValidateResourceModels(window)
}

func mergeWorkspaceResourceModels(ctx context.Context, svc *wsmeta.Service, window *forgeTypes.Window) error {
	paths, err := svc.ListRecursive(ctx, workspace.KindForgeModel)
	if err != nil {
		return nil
	}
	if window.Schemas == nil {
		window.Schemas = map[string]forgeTypes.ResourceSchema{}
	}
	if window.ResourceModels == nil {
		window.ResourceModels = map[string]forgeTypes.ResourceModel{}
	}
	windowSchemas := make(map[string]bool, len(window.Schemas))
	for name := range window.Schemas {
		windowSchemas[name] = true
	}
	windowModels := make(map[string]bool, len(window.ResourceModels))
	for name := range window.ResourceModels {
		windowModels[name] = true
	}
	schemaSources := map[string]string{}
	modelSources := map[string]string{}
	conflictedSchemas := map[string]bool{}
	conflictedModels := map[string]bool{}
	for _, modelPath := range paths {
		var registry struct {
			Schemas        map[string]forgeTypes.ResourceSchema `yaml:"schemas"`
			ResourceModels map[string]forgeTypes.ResourceModel  `yaml:"resourceModels"`
		}
		if err := svc.Load(ctx, modelPath, &registry); err != nil {
			log.Printf("workspace forge: skipping invalid resource model asset %s: %v", workspaceAssetPath(modelPath), err)
			continue
		}
		// Global model files are lazy assets. Validate each registry before it is
		// merged so an invalid Advertiser (or Campaign/Order) registry cannot make
		// an unrelated requested window fail. A window that actually references a
		// quarantined model still fails closed during the final effective-window
		// validation below with an unknown-model error.
		probe := &forgeTypes.Window{
			Schemas:        registry.Schemas,
			ResourceModels: registry.ResourceModels,
		}
		if err := forgeTypes.ValidateResourceModelStructure(probe); err != nil {
			log.Printf("workspace forge: quarantining invalid resource model asset %s: %v", workspaceAssetPath(modelPath), err)
			continue
		}
		for name, schema := range registry.Schemas {
			if conflictedSchemas[name] {
				continue
			}
			if existing, ok := window.Schemas[name]; ok && !reflect.DeepEqual(existing, schema) {
				if windowSchemas[name] {
					log.Printf("workspace forge: ignoring global resource schema %q from %s because the requested window owns that name", name, workspaceAssetPath(modelPath))
					continue
				}
				log.Printf("workspace forge: quarantining conflicting resource schema %q from %s and %s", name, workspaceAssetPath(schemaSources[name]), workspaceAssetPath(modelPath))
				delete(window.Schemas, name)
				conflictedSchemas[name] = true
				continue
			}
			window.Schemas[name] = schema
			schemaSources[name] = modelPath
		}
		for name, model := range registry.ResourceModels {
			if conflictedModels[name] {
				continue
			}
			if existing, ok := window.ResourceModels[name]; ok && !reflect.DeepEqual(existing, model) {
				if windowModels[name] {
					log.Printf("workspace forge: ignoring global resource model %q from %s because the requested window owns that name", name, workspaceAssetPath(modelPath))
					continue
				}
				log.Printf("workspace forge: quarantining conflicting resource model %q from %s and %s", name, workspaceAssetPath(modelSources[name]), workspaceAssetPath(modelPath))
				delete(window.ResourceModels, name)
				conflictedModels[name] = true
				continue
			}
			window.ResourceModels[name] = model
			modelSources[name] = modelPath
		}
	}
	return nil
}

type workspaceDialogAsset struct {
	path   string
	dialog forgeTypes.Dialog
}

func loadWorkspaceDialogs(ctx context.Context, svc *wsmeta.Service) ([]workspaceDialogAsset, error) {
	paths, err := svc.List(ctx, workspace.KindForgeDialog)
	if err != nil {
		return nil, nil
	}
	result := make([]workspaceDialogAsset, 0, len(paths))
	for _, dialogPath := range paths {
		var dialog forgeTypes.Dialog
		if err := svc.Load(ctx, filepath.Clean(dialogPath), &dialog); err != nil {
			log.Printf("workspace forge: skipping invalid dialog asset %s: %v", workspaceAssetPath(dialogPath), err)
			continue
		}
		result = append(result, workspaceDialogAsset{path: dialogPath, dialog: dialog})
	}
	return result, nil
}

func loadWorkspaceDataSources(ctx context.Context, svc *wsmeta.Service) (map[string]forgeTypes.DataSource, error) {
	paths, err := svc.ListRecursive(ctx, workspace.KindForgeDataSource)
	if err != nil {
		return nil, nil
	}
	result := make(map[string]forgeTypes.DataSource, len(paths))
	sources := make(map[string]string, len(paths))
	conflicted := map[string]bool{}
	for _, dataSourcePath := range paths {
		var identity struct {
			ID string `yaml:"id"`
		}
		if err := svc.Load(ctx, dataSourcePath, &identity); err != nil {
			log.Printf("workspace forge: skipping invalid datasource identity %s: %v", workspaceAssetPath(dataSourcePath), err)
			continue
		}
		var dataSource forgeTypes.DataSource
		if err := svc.Load(ctx, dataSourcePath, &dataSource); err != nil {
			log.Printf("workspace forge: skipping invalid datasource asset %s: %v", workspaceAssetPath(dataSourcePath), err)
			continue
		}
		id := strings.TrimSpace(identity.ID)
		if id == "" && strings.TrimSpace(dataSource.DataSourceRef) != "" {
			id = strings.TrimSpace(dataSource.DataSourceRef)
		}
		if id == "" {
			id = strings.TrimSpace(filepath.Base(strings.TrimSuffix(dataSourcePath, filepath.Ext(dataSourcePath))))
		}
		if id == "" {
			continue
		}
		if conflicted[id] {
			log.Printf("workspace forge: skipping additional duplicate datasource id %q from %s", id, workspaceAssetPath(dataSourcePath))
			continue
		}
		if dataSource.Service == nil {
			dataSource.Service = &forgeTypes.Service{
				Endpoint: "agentlyAPI",
				URI:      "/v1/api/datasources/" + id + "/fetch",
				Method:   "POST",
			}
			if dataSource.Selectors == nil {
				dataSource.Selectors = &forgeTypes.Selectors{}
			}
			if strings.TrimSpace(dataSource.Selectors.Data) == "" || strings.TrimSpace(dataSource.Selectors.Data) == "data" {
				dataSource.Selectors.Data = "rows"
			}
			if strings.TrimSpace(dataSource.Selectors.DataInfo) == "" || strings.TrimSpace(dataSource.Selectors.DataInfo) == "meta" {
				dataSource.Selectors.DataInfo = "dataInfo"
			}
		}
		if _, exists := result[id]; exists {
			log.Printf("workspace forge: quarantining duplicate datasource id %q from %s and %s", id, workspaceAssetPath(sources[id]), workspaceAssetPath(dataSourcePath))
			delete(result, id)
			delete(sources, id)
			conflicted[id] = true
			continue
		}
		result[id] = dataSource
		sources[id] = dataSourcePath
	}
	return result, nil
}

func workspaceAssetPath(assetPath string) string {
	cleaned := filepath.Clean(assetPath)
	if relative, err := filepath.Rel(workspace.Root(), cleaned); err == nil && relative != "." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(cleaned)
}
