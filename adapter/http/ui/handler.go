package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	identity "github.com/viant/agently-core/protocol/resource"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	policy "github.com/viant/agently-core/service/policy"
	forgeservice "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	windowloader "github.com/viant/agently-core/service/ui/window"
	forgeHandlers "github.com/viant/forge/backend/handlers"
	metaSvc "github.com/viant/forge/backend/service/meta"
	forgeTypes "github.com/viant/forge/backend/types"
)

const permissionApplyTimeout = 12 * time.Second

// NewEmbeddedHandler builds a UI http.Handler backed by an embedded filesystem.
// root should use the "embed:///" scheme (e.g. "embed:///metadata").
func NewEmbeddedHandler(root string, efs *embed.FS) http.Handler {
	return newHandler(root, efs)
}

// NewEmbeddedHandlerWithAuthorization binds one host's runtimes to its
// metadata handler rather than reading process-global defaults.
func NewEmbeddedHandlerWithAuthorization(root string, efs *embed.FS, admission *policy.Runtime, capabilities *permittedview.Runtime, options ...HandlerOption) http.Handler {
	return buildHandler(root, efs, admission, capabilities, false, options...)
}

func newHandler(root string, efs *embed.FS) http.Handler {
	return buildHandler(root, efs, nil, nil, true)
}

func newHandlerWithAuthorization(root string, efs *embed.FS, admission *policy.Runtime, capabilities *permittedview.Runtime) http.Handler {
	return buildHandler(root, efs, admission, capabilities, false)
}

func buildHandler(root string, efs *embed.FS, admission *policy.Runtime, capabilities *permittedview.Runtime, useDefaults bool, options ...HandlerOption) http.Handler {
	settings := handlerOptions{}
	for _, option := range options {
		if option != nil {
			option(&settings)
		}
	}
	mux := http.NewServeMux()
	var rootMSvc *metaSvc.Service
	if efs == nil {
		rootMSvc = metaSvc.New(afs.New(), root)
	} else {
		rootMSvc = metaSvc.New(afs.New(), root, efs)
	}
	mux.HandleFunc("/navigation", navigationHandlerWithAuthorization(rootMSvc, root, admission, useDefaults, settings.windows))

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
		var aWindow *forgeTypes.Window
		canonical := settings.windows != nil && settings.windows.UsesWindowResourceResolution()
		if canonical {
			var err error
			aWindow, err = resolvedHTTPWindow(r, settings.windows, strings.Join(pathParts, "/"))
			if err != nil {
				http.Error(w, "window not found or access denied", http.StatusNotFound)
				return
			}
		} else {
			runtime := admission
			if runtime == nil && useDefaults {
				runtime = policy.DefaultRuntime()
			}
			if runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
				err := runtime.Authorize(r.Context(), policy.OperationWindowView,
					strings.TrimSpace(r.URL.Query().Get("conversationId")),
					policy.Candidate{ID: windowKey, Kind: "window"}, nil)
				if errors.Is(err, policy.ErrIdentityRejected) {
					http.Error(w, "authentication required", http.StatusUnauthorized)
					return
				}
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
			var workspaceErr error
			aWindow, workspaceErr = windowloader.LoadWorkspaceWindow(r.Context(), windowKey, target)
			if workspaceErr != nil {
				if errors.Is(workspaceErr, policy.ErrDenied) {
					http.Error(w, "window not found", http.StatusNotFound)
					return
				}
				http.Error(w, workspaceErr.Error(), http.StatusInternalServerError)
				return
			}
			if aWindow == nil {
				// Built-in windows are loaded from the application metadata root and
				// receive only the workspace assets their own resources block
				// assigns. Workspace windows are already merged by LoadWorkspaceWindow.
				var err error
				aWindow, err = forgeHandlers.LoadWindow(r.Context(), windowMSvc, windowRoot, windowKey, subPath, target)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				assignment, err := windowloader.LoadResourceAssignment(r.Context(), windowMSvc, windowRoot, windowKey, subPath, target)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if err := windowloader.MergeWorkspaceForgeAssets(r.Context(), aWindow, assignment); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
			}

		}
		var authorization *permittedview.Snapshot
		var openOriginal *forgeTypes.Window
		var openParameters map[string]any
		var openLease time.Time
		if aWindow.Authorization != nil && applyPermissionRequested(r) {
			runtime := capabilities
			if runtime == nil && useDefaults {
				runtime = permittedview.DefaultRuntime()
			}
			if runtime == nil && !canonical {
				http.Error(w, "permitted-view authorization runtime is unavailable", http.StatusServiceUnavailable)
				return
			}
			parameters, err := windowParametersFromRequest(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if rawVersion := strings.TrimSpace(r.URL.Query().Get("authorizationSchemaVersion")); rawVersion != "" {
				version, e := strconv.Atoi(rawVersion)
				if e != nil || version != permittedview.SchemaVersion(aWindow.Authorization) {
					http.Error(w, "authorization schema version mismatch", http.StatusBadRequest)
					return
				}
			}
			if canonical {
				// Caller resourceData never supplies protected bootstrap authority.
				checker, ok := settings.windows.(interface {
					AdmitWindowOpenDecision(context.Context, *forgeTypes.Window, map[string]any) (*forgeservice.WindowOpenDecision, error)
				})
				if !ok {
					http.Error(w, "window open admission is unavailable", http.StatusForbidden)
					return
				}
				admissionContext, cancel := context.WithTimeout(r.Context(), permissionApplyTimeout)
				admitted, err := checker.AdmitWindowOpenDecision(admissionContext, aWindow, parameters)
				cancel()
				if err != nil || admitted == nil || admitted.Window == nil {
					writeOpenAdmissionError(w, err)
					return
				}
				openOriginal, openParameters, openLease = aWindow, parameters, admitted.ValidUntil
				aWindow = admitted.Window
				if len(aWindow.AuthorizationSnapshot) > 0 {
					raw, _ := json.Marshal(aWindow.AuthorizationSnapshot)
					authorization = &permittedview.Snapshot{}
					if json.Unmarshal(raw, authorization) != nil {
						http.Error(w, "window authorization is unavailable", http.StatusForbidden)
						return
					}
				}
			} else {
				var resourceData map[string]any
				if aWindow.Authorization.Resource != nil && strings.EqualFold(aWindow.Authorization.Resource.ID.Source, "resource") {
					http.Error(w, "trusted resource bootstrap is unavailable", http.StatusForbidden)
					return
				}
				windowID := strings.TrimSpace(r.URL.Query().Get("windowId"))
				conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
				applyContext := r.Context()
				if canonical {
					if aWindow.Resource == nil {
						http.Error(w, "window resource unavailable", http.StatusNotFound)
						return
					}
					applyContext = runtimerequestctx.WithResolvedResource(applyContext, *aWindow.Resource)
					applyContext = runtimerequestctx.WithWindowTarget(applyContext, aWindow.ResourceTarget)
				}
				if conversationID != "" {
					applyContext = runtimerequestctx.WithConversationID(applyContext, conversationID)
				}
				bound, err := permittedview.BindResource(aWindow, windowID, conversationID, parameters, resourceData)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if rawVersion := strings.TrimSpace(r.URL.Query().Get("authorizationSchemaVersion")); rawVersion != "" {
					version, parseErr := strconv.Atoi(rawVersion)
					if parseErr != nil || version != bound.SchemaVersion {
						http.Error(w, "authorization schema version mismatch", http.StatusBadRequest)
						return
					}
				}
				permissionContext, cancel := context.WithTimeout(applyContext, permissionApplyTimeout)
				compiled, err := runtime.Apply(permissionContext, bound)
				cancel()
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(permissionContext.Err(), context.DeadlineExceeded) {
						http.Error(w, "permission service timed out", http.StatusGatewayTimeout)
						return
					}
					if errors.Is(err, permittedview.ErrUnavailable) {
						http.Error(w, "permission service unavailable", http.StatusServiceUnavailable)
						return
					}
					http.Error(w, "resource not found or access denied", http.StatusForbidden)
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
		}
		if canonical {
			if _, err := settings.windows.WindowDefinitionGet(r.Context(), &forgeservice.WindowDefinitionGetInput{WindowID: strings.Join(pathParts, "/"), ResolvedResource: aWindow.Resource, Target: aWindow.ResourceTarget}); err != nil {
				http.Error(w, "window access changed", http.StatusForbidden)
				return
			}
		}
		if openOriginal != nil {
			checker := settings.windows.(interface {
				AdmitWindowOpenDecision(context.Context, *forgeTypes.Window, map[string]any) (*forgeservice.WindowOpenDecision, error)
			})
			if !openLease.After(time.Now()) {
				http.Error(w, "window admission expired", http.StatusForbidden)
				return
			}
			finalContext, cancel := context.WithTimeout(r.Context(), permissionApplyTimeout)
			_, err := checker.AdmitWindowOpenDecision(finalContext, openOriginal, openParameters)
			cancel()
			if err != nil {
				writeOpenAdmissionError(w, err)
				return
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
	return navigationHandlerWithAuthorization(loader, root, nil, true)
}

func navigationHandlerWithAuthorization(loader *metaSvc.Service, root string, admission *policy.Runtime, useDefaults bool, providers ...WindowResourceProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := forgeHandlers.FetchNavigationData(r.Context(), loader, root, targetContextFromRequest(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(providers) > 0 && providers[0] != nil && providers[0].UsesWindowResourceResolution() {
			allowed := map[string]bool{}
			for offset := 0; ; {
				page, err := providers[0].WindowDefinitionsList(r.Context(), &forgeservice.WindowDefinitionListInput{Limit: 100, Offset: offset})
				if err != nil || page == nil {
					http.Error(w, "window authorization unavailable", http.StatusServiceUnavailable)
					return
				}
				for _, entry := range page.Windows {
					allowed[entry.WindowID] = true
				}
				if !page.HasMore {
					break
				}
				if len(page.Windows) == 0 {
					http.Error(w, "invalid window catalog page", http.StatusServiceUnavailable)
					return
				}
				offset += len(page.Windows)
			}
			items = filterNavigationItemsWithMode(items, allowed, true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(forgeHandlers.NavigationResponse{Status: "ok", Data: items})
			return
		}
		runtime := admission
		if runtime == nil && useDefaults {
			runtime = policy.DefaultRuntime()
		}
		if runtime != nil && runtime.IsEnabled(policy.OperationWindowView) {
			candidates := navigationWindowCandidatesWithMode(items, runtime.ExactIDs)
			allowedCandidates, policyErr := runtime.Filter(r.Context(), policy.OperationWindowView,
				strings.TrimSpace(r.URL.Query().Get("conversationId")), candidates, nil)
			if errors.Is(policyErr, policy.ErrIdentityRejected) {
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			if errors.Is(policyErr, policy.ErrDenied) {
				allowedCandidates = nil
			} else if policyErr != nil {
				http.Error(w, "window authorization unavailable", http.StatusServiceUnavailable)
				return
			}
			allowed := make(map[string]bool, len(allowedCandidates))
			for _, candidate := range allowedCandidates {
				allowed[windowKey(candidate.ID, runtime.ExactIDs)] = true
			}
			items = filterNavigationItemsWithMode(items, allowed, runtime.ExactIDs)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(forgeHandlers.NavigationResponse{Status: "ok", Data: items})
	}
}

func navigationWindowCandidates(items []forgeTypes.NavigationItem) []policy.Candidate {
	return navigationWindowCandidatesWithMode(items, false)
}

func windowKey(value string, exact bool) string {
	if exact {
		return value
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func navigationWindowCandidatesWithMode(items []forgeTypes.NavigationItem, exact bool) []policy.Candidate {
	seen := map[string]bool{}
	var result []policy.Candidate
	var visit func([]forgeTypes.NavigationItem)
	visit = func(entries []forgeTypes.NavigationItem) {
		for _, item := range entries {
			key := strings.TrimSpace(item.WindowKey)
			normalized := windowKey(key, exact)
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
	return filterNavigationItemsWithMode(items, allowed, false)
}

func filterNavigationItemsWithMode(items []forgeTypes.NavigationItem, allowed map[string]bool, exact bool) []forgeTypes.NavigationItem {
	result := make([]forgeTypes.NavigationItem, 0, len(items))
	for _, item := range items {
		key := windowKey(strings.TrimSpace(item.WindowKey), exact)
		if key != "" && !allowed[key] {
			continue
		}
		item.ChildNodes = filterNavigationItemsWithMode(item.ChildNodes, allowed, exact)
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

func writeOpenAdmissionError(w http.ResponseWriter, err error) {
	status, message := http.StatusForbidden, "resource not found or access denied"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "permission service timed out"
	case errors.Is(err, permittedview.ErrUnavailable):
		status, message = http.StatusServiceUnavailable, "permission service unavailable"
	case errors.Is(err, identity.ErrResource):
		status, message = http.StatusBadRequest, "invalid window parameters"
	}
	http.Error(w, message, status)
}
