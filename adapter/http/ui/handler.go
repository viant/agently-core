package ui

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	windowloader "github.com/viant/agently-core/service/ui/window"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
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
			workspaceWindow, workspaceErr := windowloader.LoadWorkspaceWindow(r.Context(), windowKey, target)
			if workspaceErr != nil || workspaceWindow == nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			aWindow = workspaceWindow
		}
		if err := windowloader.MergeWorkspaceForgeAssets(r.Context(), aWindow); err != nil {
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
