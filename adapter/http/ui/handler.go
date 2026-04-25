package ui

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/workspace"
	wsmeta "github.com/viant/agently-core/workspace/service/meta"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

// NewEmbeddedHandler builds a UI http.Handler backed by an embedded filesystem.
// root should use the "embed:///" scheme (e.g. "embed:///metadata").
func NewEmbeddedHandler(root string, efs *embed.FS) http.Handler {
	return newHandler(root, efs)
}

func newHandler(root string, efs *embed.FS) http.Handler {
	mux := http.NewServeMux()
	var rootMSvc *metaSvc.Service
	if efs == nil {
		rootMSvc = metaSvc.New(afs.New(), root)
	} else {
		rootMSvc = metaSvc.New(afs.New(), root, efs)
	}
	mux.HandleFunc("/navigation", forgeHandlers.NavigationHandler(rootMSvc, root))

	windowBase := "/window/"
	windowRoot := root
	if !strings.HasSuffix(windowRoot, "/") {
		windowRoot += "/"
	}
	windowRoot = url.Join(windowRoot, "window")
	var windowMSvc *metaSvc.Service
	if efs == nil {
		windowMSvc = metaSvc.New(afs.New(), windowRoot)
	} else {
		windowMSvc = metaSvc.New(afs.New(), windowRoot, efs)
	}
	mux.HandleFunc(windowBase, func(w http.ResponseWriter, r *http.Request) {
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, windowBase), "/")
		if len(pathParts) < 1 || pathParts[0] == "" {
			http.Error(w, "missing path in URL", http.StatusBadRequest)
			return
		}
		windowKey := pathParts[0]
		subPath := strings.Join(pathParts[1:], "/")
		target := targetContextFromRequest(r)
		aWindow, err := forgeHandlers.LoadWindow(r.Context(), windowMSvc, windowRoot, windowKey, subPath, target)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := mergeWorkspaceForgeAssets(r.Context(), aWindow); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(forgeHandlers.WindowResponse{
			Status: "ok",
			Data:   aWindow,
		})
	})

	return mux
}

func targetContextFromRequest(r *http.Request) *metaSvc.TargetContext {
	if r == nil {
		return nil
	}
	query := r.URL.Query()
	capabilities := query["capabilities"]
	if len(capabilities) == 1 && strings.Contains(capabilities[0], ",") {
		capabilities = strings.Split(capabilities[0], ",")
	}
	for index, value := range capabilities {
		capabilities[index] = strings.TrimSpace(value)
	}
	return &metaSvc.TargetContext{
		Platform:     strings.TrimSpace(query.Get("platform")),
		FormFactor:   strings.TrimSpace(query.Get("formFactor")),
		Surface:      strings.TrimSpace(query.Get("surface")),
		Capabilities: compactStrings(capabilities),
	}
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func mergeWorkspaceForgeAssets(ctx context.Context, window *forgeTypes.Window) error {
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
	return nil
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
