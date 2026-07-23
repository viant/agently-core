package window

import (
	"context"
	"fmt"
	"path/filepath"
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
		window, err := forgeHandlers.LoadWindow(ctx, loader, workspaceWindowRoot, windowKey, "", target)
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
	if window == nil || window.View.Content == nil || window.View.Content.Dashboard == nil {
		return nil
	}
	raw := window.View.Content.Dashboard.ReportBuilder["actionRefs"]
	result := make([]string, 0)
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
		for _, dialog := range dialogs {
			id := strings.TrimSpace(dialog.Id)
			if id == "" || existing[id] {
				continue
			}
			window.Dialogs = append(window.Dialogs, dialog)
			existing[id] = true
		}
	}

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
	return enrichWorkspaceWindow(ctx, window)
}

func loadWorkspaceDialogs(ctx context.Context, svc *wsmeta.Service) ([]forgeTypes.Dialog, error) {
	paths, err := svc.List(ctx, workspace.KindForgeDialog)
	if err != nil {
		return nil, nil
	}
	result := make([]forgeTypes.Dialog, 0, len(paths))
	for _, dialogPath := range paths {
		var dialog forgeTypes.Dialog
		if err := svc.Load(ctx, filepath.Clean(dialogPath), &dialog); err != nil {
			return nil, err
		}
		result = append(result, dialog)
	}
	return result, nil
}

func loadWorkspaceDataSources(ctx context.Context, svc *wsmeta.Service) (map[string]forgeTypes.DataSource, error) {
	paths, err := svc.List(ctx, workspace.KindForgeDataSource)
	if err != nil {
		return nil, nil
	}
	result := make(map[string]forgeTypes.DataSource, len(paths))
	for _, dataSourcePath := range paths {
		var dataSource forgeTypes.DataSource
		if err := svc.Load(ctx, filepath.Clean(dataSourcePath), &dataSource); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(filepath.Base(strings.TrimSuffix(dataSourcePath, filepath.Ext(dataSourcePath))))
		if strings.TrimSpace(dataSource.DataSourceRef) != "" {
			id = strings.TrimSpace(dataSource.DataSourceRef)
		}
		if id == "" {
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
		result[id] = dataSource
	}
	return result, nil
}
