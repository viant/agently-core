package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viant/afs"
	"github.com/viant/afs/url"
	"github.com/viant/agently-core/protocol/primitive/observe"
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
		if tryPublishedWindow(w, r, settings.published, pathParts) {
			return
		}
		var aWindow *forgeTypes.Window
		canonical := settings.windows != nil && settings.windows.UsesWindowResourceResolution()
		if canonical {
			var err error
			span := observe.Start(r.Context(), observe.HTTPWindowResolve, observe.Labels{Kind: "window"})
			aWindow, err = resolvedHTTPWindow(r, settings.windows, strings.Join(pathParts, "/"))
			span.End(observe.OutcomeFor(err), observe.CacheUnknown)
			if err != nil {
				log.Printf("window metadata lookup failed: %v", err)
				switch {
				case errors.Is(err, context.DeadlineExceeded), errors.Is(err, forgeservice.ErrWindowSelectionUnavailable):
					writeWindowError(w, err)
				default:
					http.Error(w, "window not found or access denied", http.StatusNotFound)
				}
				return
			}
		} else {
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
		if aWindow != nil {
			copy := *aWindow
			copy.SelectionBinding = nil
			copy.LocalStateOwner = nil
			aWindow = &copy
		}
		if r.URL.Query().Get("expectedResource") != "" {
			if !canonical || !applyPermissionRequested(r) || r.Header.Get("Mcp-Session-Id") == "" || r.URL.Query().Get("windowId") == "" || r.URL.Query().Get("clientId") == "" || checkExpectedWindowResource(r, aWindow.Resource) != nil {
				writeWindowError(w, identity.ErrResourceDenied)
				return
			}
		}
		var authorization *permittedview.Snapshot
		var openParameters map[string]any
		var presentationResource map[string]any
		if canonical && !applyPermissionRequested(r) {
			projectionSpan := observe.Start(r.Context(), "http.window.project", observe.Labels{Kind: "window"})
			var projected *permittedview.Result
			var err error
			configured, hasConfigured := settings.windows.(interface{ HasWindowUIProjection() bool })
			project, hasProjector := settings.windows.(interface {
				ProjectWindowWithInput(context.Context, *permittedview.BoundView) (*forgeTypes.Window, error)
			})
			if aWindow.AuthorizationSnapshot == nil && hasConfigured && configured.HasWindowUIProjection() && hasProjector {
				var window *forgeTypes.Window
				window, err = project.ProjectWindowWithInput(r.Context(), &permittedview.BoundView{Window: aWindow})
				projected = &permittedview.Result{Window: window}
			} else {
				runtime := capabilities
				if runtime == nil && useDefaults {
					runtime = permittedview.DefaultRuntime()
				}
				projected, err = runtime.ProjectPrincipal(r.Context(), aWindow)
			}
			projectionSpan.End(observe.OutcomeFor(err), observe.CacheUnknown)
			if err != nil || projected == nil || projected.Window == nil {
				writeWindowError(w, err)
				return
			}
			aWindow = projected.Window
			for _, diagnostic := range projected.Diagnostics {
				log.Printf("window presentation: %s", diagnostic.Code)
			}
		}
		if applyPermissionRequested(r) {
			if rawVersion := strings.TrimSpace(r.URL.Query().Get("authorizationSchemaVersion")); rawVersion != "" && aWindow.Authorization != nil {
				version, err := strconv.Atoi(rawVersion)
				if err != nil || version != permittedview.SchemaVersion(aWindow.Authorization) {
					http.Error(w, "authorization schema version mismatch", http.StatusBadRequest)
					return
				}
			}
			var err error
			openParameters, err = windowParametersFromRequest(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			presentationResource, err = resourceDataFromRequest(r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			runtime := capabilities
			if runtime == nil && useDefaults {
				runtime = permittedview.DefaultRuntime()
			}
			projectionSpan := observe.Start(r.Context(), "http.window.project", observe.Labels{Kind: "window"})
			if canonical {
				if project, ok := settings.windows.(interface {
					ProjectWindowWithInput(context.Context, *permittedview.BoundView) (*forgeTypes.Window, error)
				}); ok {
					aWindow, err = project.ProjectWindowWithInput(r.Context(), &permittedview.BoundView{Window: aWindow, WindowID: aWindow.WindowKey, WindowForm: openParameters, ResourceData: presentationResource})
				}
			} else {
				var result *permittedview.Result
				bound := &permittedview.BoundView{Window: aWindow, WindowID: aWindow.WindowKey, WindowForm: openParameters, ResourceData: presentationResource}
				result, err = runtime.Project(r.Context(), aWindow, bound)
				if result != nil {
					aWindow = result.Window
					authorization = result.Authorization
				}
			}
			projectionSpan.End(observe.OutcomeFor(err), observe.CacheUnknown)
			if err != nil || aWindow == nil {
				writeWindowError(w, err)
				return
			}
			if canonical {
				if emitter, ok := settings.windows.(interface {
					EmitWindowSelection(context.Context, *forgeTypes.Window, map[string]any) (*forgeTypes.Window, error)
				}); ok {
					selectionSpan := observe.Start(r.Context(), "http.window.selection", observe.Labels{Kind: "window"})
					aWindow, err = emitter.EmitWindowSelection(r.Context(), aWindow, openParameters)
					selectionSpan.End(observe.OutcomeFor(err), observe.CacheUnknown)
					if err != nil || aWindow == nil {
						writeWindowError(w, err)
						return
					}
				}
			}
		}
		// A successful provider read selects the delivered definition, including
		// any authored UI authorization hints. Bind its negotiated UI instance
		// without requiring a separate browser OPEN/projection request.
		if canonical && (applyPermissionRequested(r) || r.Header.Get("Mcp-Session-Id") != "") && (r.URL.Query().Get("clientId") != "" || r.URL.Query().Get("windowId") != "") {
			binder, ok := settings.windows.(interface {
				BindAdmittedFrameworkWindow(context.Context, string, string, string, *forgeTypes.Window) error
			})
			if !ok {
				writeWindowError(w, identity.ErrResourceDenied)
				return
			}
			bindContext := r.Context()
			bindContext = runtimerequestctx.WithUIBridgeSessionID(bindContext, r.Header.Get("Mcp-Session-Id"))
			handled := false
			if native, ok := settings.windows.(interface {
				BindAdmittedNativePresentationWindow(context.Context, string, string, string, *forgeTypes.Window) (bool, error)
			}); ok {
				var err error
				bindSpan := observe.Start(bindContext, observe.HTTPWindowBind, observe.Labels{Kind: "window"})
				handled, err = native.BindAdmittedNativePresentationWindow(bindContext, r.URL.Query().Get("clientId"), r.URL.Query().Get("windowId"), strings.Join(pathParts, "/"), aWindow)
				bindSpan.End(observe.OutcomeFor(err), observe.CacheUnknown)
				if err != nil {
					writeWindowError(w, err)
					return
				}
			}
			if !handled {
				if err := binder.BindAdmittedFrameworkWindow(bindContext, r.URL.Query().Get("clientId"), r.URL.Query().Get("windowId"), strings.Join(pathParts, "/"), aWindow); err != nil {
					writeWindowError(w, err)
					return
				}
			}
		}
		if owner, ok := settings.windows.(interface {
			AttachLocalStateOwner(context.Context, *forgeTypes.Window) (*forgeTypes.Window, error)
		}); ok {
			var err error
			aWindow, err = owner.AttachLocalStateOwner(r.Context(), aWindow)
			if err != nil {
				writeWindowError(w, err)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		encodeSpan := observe.Start(r.Context(), observe.HTTPResponseEncode, observe.Labels{Kind: "window"})
		encodeErr := json.NewEncoder(w).Encode(permittedWindowResponse{
			Status:        "ok",
			Data:          aWindow,
			Authorization: authorization,
		})
		encodeSpan.End(observe.OutcomeFor(encodeErr), observe.CacheBypass)
	})

	return manager.WithRequestCatalog(mux)
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
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("invalid windowParams: %w", err)
		}
		if decoder.Decode(new(any)) != io.EOF {
			return nil, fmt.Errorf("invalid windowParams")
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

func writeWindowError(w http.ResponseWriter, err error) {
	status, message := http.StatusForbidden, "resource not found or access denied"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status, message = http.StatusGatewayTimeout, "permission service timed out"
	case errors.Is(err, permittedview.ErrUnavailable) || errors.Is(err, forgeservice.ErrWindowSelectionUnavailable):
		status, message = http.StatusServiceUnavailable, "permission service unavailable"
	case errors.Is(err, identity.ErrResource):
		status, message = http.StatusBadRequest, "invalid window parameters"
	}
	http.Error(w, message, status)
}
