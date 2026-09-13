package ui

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	policy "github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/ui/permittedview"
	windowloader "github.com/viant/agently-core/service/ui/window"
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
	mux.HandleFunc("/navigation", navigationHandler(rootMSvc, root))

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
		windowKey := strings.TrimSpace(pathParts[0])
		if windowKey == "" {
			http.Error(w, "window key is required", http.StatusBadRequest)
			return
		}
		if runtime := policy.DefaultRuntime(); runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
			err := runtime.Authorize(r.Context(), policy.OperationWindowView,
				strings.TrimSpace(r.URL.Query().Get("conversationId")),
				policy.Candidate{ID: windowKey, Kind: "window"}, nil)
			if errors.Is(err, policy.ErrDenied) {
				http.Error(w, "window not found", http.StatusNotFound)
				return
			}
			if err != nil {
				http.Error(w, "window authorization unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		subPath := strings.Join(pathParts[1:], "/")
		target := targetContextFromRequest(r)
		aWindow, workspaceErr := windowloader.LoadWorkspaceWindow(r.Context(), windowKey, target)
		if workspaceErr != nil {
			http.Error(w, workspaceErr.Error(), http.StatusInternalServerError)
			return
		}
		if aWindow == nil {
			var err error
			aWindow, err = forgeHandlers.LoadWindow(r.Context(), windowMSvc, windowRoot, windowKey, subPath, target)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := windowloader.MergeWorkspaceForgeAssets(r.Context(), aWindow); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var authorization *permittedview.Snapshot
		if aWindow.Authorization != nil && applyPermissionRequested(r) {
			runtime := permittedview.DefaultRuntime()
			if runtime == nil {
				http.Error(w, "permitted-view authorization runtime is unavailable", http.StatusServiceUnavailable)
				return
			}
			parameters, err := windowParametersFromRequest(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resourceData, err := resourceDataFromRequest(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			windowID := strings.TrimSpace(r.URL.Query().Get("windowId"))
			conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
			applyContext := r.Context()
			if conversationID != "" {
				applyContext = runtimerequestctx.WithConversationID(applyContext, conversationID)
			}
			bound, err := permittedview.BindResource(aWindow, windowID, conversationID, parameters, resourceData)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			compiled, err := runtime.Apply(applyContext, bound)
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if compiled == nil || compiled.Denied || compiled.Window == nil {
				http.Error(w, "resource not found or access denied", http.StatusForbidden)
				return
			}
			aWindow = compiled.Window
			authorization = compiled.Authorization
			if raw, marshalErr := json.Marshal(authorization); marshalErr == nil {
				_ = json.Unmarshal(raw, &aWindow.AuthorizationSnapshot)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(permittedWindowResponse{
			Status:        "ok",
			Data:          aWindow,
			Authorization: authorization,
		})
	})

	return mux
}

func navigationHandler(loader *metaSvc.Service, root string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := forgeHandlers.FetchNavigationData(r.Context(), loader, root, targetContextFromRequest(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		runtime := policy.DefaultRuntime()
		if runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
			candidates := navigationWindowCandidates(items)
			allowedCandidates, policyErr := runtime.Filter(r.Context(), policy.OperationWindowView,
				strings.TrimSpace(r.URL.Query().Get("conversationId")), candidates, nil)
			if errors.Is(policyErr, policy.ErrDenied) {
				allowedCandidates = nil
			} else if policyErr != nil {
				http.Error(w, "window authorization unavailable", http.StatusServiceUnavailable)
				return
			}
			allowed := make(map[string]bool, len(allowedCandidates))
			for _, candidate := range allowedCandidates {
				allowed[strings.ToLower(strings.TrimSpace(candidate.ID))] = true
			}
			items = filterNavigationItems(items, allowed)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(forgeHandlers.NavigationResponse{Status: "ok", Data: items})
	}
}

func navigationWindowCandidates(items []forgeTypes.NavigationItem) []policy.Candidate {
	seen := map[string]bool{}
	var result []policy.Candidate
	var visit func([]forgeTypes.NavigationItem)
	visit = func(entries []forgeTypes.NavigationItem) {
		for _, item := range entries {
			key := strings.TrimSpace(item.WindowKey)
			normalized := strings.ToLower(key)
			if key != "" && !seen[normalized] {
				seen[normalized] = true
				result = append(result, policy.Candidate{ID: key, Kind: "window", Metadata: map[string]any{"title": item.WindowTitle}})
			}
			visit(item.ChildNodes)
		}
	}
	visit(items)
	return result
}

func filterNavigationItems(items []forgeTypes.NavigationItem, allowed map[string]bool) []forgeTypes.NavigationItem {
	result := make([]forgeTypes.NavigationItem, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.WindowKey))
		if key != "" && !allowed[key] {
			continue
		}
		item.ChildNodes = filterNavigationItems(item.ChildNodes, allowed)
		if key == "" && len(item.ChildNodes) == 0 {
			continue
		}
		result = append(result, item)
	}
	return result
}

func applyPermissionRequested(r *http.Request) bool {
	if r == nil {
		return false
	}
	value := strings.TrimSpace(r.URL.Query().Get("applyPermission"))
	return strings.EqualFold(value, "true") || value == "1"
}

type permittedWindowResponse struct {
	Status        string                  `json:"status"`
	Data          *forgeTypes.Window      `json:"data"`
	Authorization *permittedview.Snapshot `json:"authorization,omitempty"`
}

func windowParametersFromRequest(r *http.Request) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if r == nil {
		return result, nil
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("windowParams")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, fmt.Errorf("invalid windowParams: %w", err)
		}
	}
	return result, nil
}

func resourceDataFromRequest(r *http.Request) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if r == nil {
		return result, nil
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("resource")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, fmt.Errorf("invalid resource: %w", err)
		}
	}
	return result, nil
}

func targetContextFromRequest(r *http.Request) *metaSvc.TargetContext {
	if r == nil {
		return nil
	}
	query := r.URL.Query()
	capabilities := capabilityValuesFromQuery(query["capabilities"])
	return &metaSvc.TargetContext{
		Platform:     strings.TrimSpace(query.Get("platform")),
		FormFactor:   strings.TrimSpace(query.Get("formFactor")),
		Surface:      strings.TrimSpace(query.Get("surface")),
		Capabilities: capabilities,
	}
}

func capabilityValuesFromQuery(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	capabilities := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				capabilities = append(capabilities, trimmed)
			}
		}
	}
	return capabilities
}
