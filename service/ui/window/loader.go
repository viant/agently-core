package window

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	metaURL "github.com/viant/afs/url"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

// LoadWorkspaceWindow loads a workspace-owned Forge window and merges the
// workspace assets its resource assignment whitelists onto it so every
// consumer sees the same effective surface truth.
func LoadWorkspaceWindow(ctx context.Context, windowKey string, target *metaSvc.TargetContext) (*forgeTypes.Window, error) {
	return loadWorkspaceWindowAt(ctx, workspace.Root(), windowKey, target, true, enrichWorkspaceWindow)
}

// LoadWorkspaceWindowAt binds one host's trusted workspace root for the full
// window and resource-assignment load. Authz catalogs use this form so another
// process-global workspace cannot substitute assets during a request.
func LoadWorkspaceWindowAt(ctx context.Context, workspaceRoot, windowKey string, target *metaSvc.TargetContext) (*forgeTypes.Window, error) {
	return loadWorkspaceWindowAt(ctx, workspaceRoot, windowKey, target, false, nil)
}

// LoadWorkspaceWindowWithEnricherAt supplies the host's extension callback
// explicitly. The protected Forge catalog uses this form for report windows.
func LoadWorkspaceWindowWithEnricherAt(ctx context.Context, workspaceRoot, windowKey string, target *metaSvc.TargetContext, enricher WorkspaceWindowEnricher) (*forgeTypes.Window, error) {
	return loadWorkspaceWindowAt(ctx, workspaceRoot, windowKey, target, false, enricher)
}

func loadWorkspaceWindowAt(ctx context.Context, workspaceRoot, windowKey string, target *metaSvc.TargetContext, allowRemote bool, enricher WorkspaceWindowEnricher) (*forgeTypes.Window, error) {
	return loadWorkspaceWindowWithOptionsAt(ctx, workspaceRoot, windowKey, target, allowRemote, enricher, LoaderOptions{})
}

// LoadWorkspaceWindowWithOptionsAt preserves native materialization with a host filesystem.
func LoadWorkspaceWindowWithOptionsAt(ctx context.Context, workspaceRoot, windowKey string, target *metaSvc.TargetContext, enricher WorkspaceWindowEnricher, options LoaderOptions) (*forgeTypes.Window, error) {
	return loadWorkspaceWindowWithOptionsAt(ctx, workspaceRoot, windowKey, target, false, enricher, options)
}

func loadWorkspaceWindowWithOptionsAt(ctx context.Context, workspaceRoot, windowKey string, target *metaSvc.TargetContext, allowRemote bool, enricher WorkspaceWindowEnricher, options LoaderOptions) (result *forgeTypes.Window, failure error) {
	defer func() {
		if err := options.check(); err != nil {
			result = nil
			failure = err
		}
	}()
	if strings.TrimSpace(workspaceRoot) == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	windowKey = strings.TrimSpace(windowKey)
	if windowKey == "" {
		return nil, nil
	}
	if providerID, remoteKey, ok := ParseRemoteWindowKey(windowKey); ok {
		if !allowRemote {
			return nil, fmt.Errorf("remote window requires a host-owned definition resolver")
		}
		return LoadRemoteWindow(ctx, providerID, remoteKey)
	}
	if !validBoundWindowPath(windowKey) {
		return nil, fmt.Errorf("invalid workspace window key")
	}
	workspaceWindowRoot := "file://" + filepath.ToSlash(filepath.Join(workspaceRoot, workspace.KindForgeWindow))
	loader := metaSvc.New(options.filesystem(), workspaceWindowRoot)
	if target == nil && options.SharedDefault {
		direct, err := loader.Exists(ctx, metaURL.Join(workspaceWindowRoot, windowKey, "main.yaml"))
		if err != nil {
			return nil, err
		}
		singleton, err := loader.Exists(ctx, metaURL.Join(workspaceWindowRoot, windowKey+".yaml"))
		if err != nil {
			return nil, err
		}
		if !direct && !singleton {
			target = &metaSvc.TargetContext{}
		}
	}
	if resolvedBase, err := loader.ResolveWindowBase(ctx, metaURL.Join(workspaceWindowRoot, windowKey, "main"), target); err == nil {
		window, err := forgeHandlers.LoadWindowStructure(ctx, loader, workspaceWindowRoot, windowKey, "", target)
		if err != nil {
			return nil, err
		}
		if window == nil || window.View.Content == nil {
			return nil, fmt.Errorf("workspace forge window %q has no view content", windowKey)
		}
		assignment, err := loadResourceAssignmentFromBase(ctx, loader, resolvedBase, target)
		if err != nil {
			return nil, err
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
		if err := mergeWorkspaceForgeAssetsWithOptionsAt(ctx, workspaceRoot, window, assignment, enricher, options); err != nil {
			return nil, err
		}
		return window, nil
	}

	svc := wsmeta.New(options.filesystem(), workspaceRoot)
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
	holder := &resourceAssignmentHolder{}
	if err := svc.Load(ctx, windowPath, holder); err != nil {
		return nil, fmt.Errorf("failed to load resource assignment for window %s: %w", windowKey, err)
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
	if err := mergeWorkspaceForgeAssetsWithOptionsAt(ctx, workspaceRoot, window, holder.Resources, enricher, options); err != nil {
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
