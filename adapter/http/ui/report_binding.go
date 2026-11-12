package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
)

// Restored reports use the same authenticated instance binding as windows.
// This read never executes report data or writes a conversation event.
func bindHTTPReport(w http.ResponseWriter, r *http.Request, provider WindowResourceProvider, key string) bool {
	if !strings.HasPrefix(key, "report://") {
		return false
	}
	source, ok := provider.(interface {
		ReportDefinition(context.Context, *service.ReportDefinitionInput) (*service.ReportDefinitionOutput, error)
		BindAdmittedNativePresentationWindow(context.Context, string, string, string, *types.Window) (bool, error)
	})
	if !ok || !applyPermissionRequested(r) || r.Header.Get("Mcp-Session-Id") == "" || r.URL.Query().Get("clientId") == "" || r.URL.Query().Get("windowId") == "" {
		writeWindowError(w, identity.ErrResourceDenied)
		return true
	}
	var pin *identity.ResolvedResource
	if err := decodeResourceQuery(r, "resolvedResource", &pin); err != nil {
		writeWindowError(w, err)
		return true
	}
	ref := identity.ResourceRef{URI: key}
	if pin != nil {
		if pin.URI != key {
			writeWindowError(w, identity.ErrResourceDenied)
			return true
		}
		ref.Revision = pin.Selector()
	}
	ctx := requestctx.WithUIBridgeSessionID(r.Context(), r.Header.Get("Mcp-Session-Id"))
	selected, err := source.ReportDefinition(ctx, &service.ReportDefinitionInput{Resource: ref, ResolvedResource: pin})
	if err != nil {
		writeWindowError(w, err)
		return true
	}
	if selected == nil || selected.Resource == nil || selected.Resource.URI != key {
		writeWindowError(w, identity.ErrResourceDenied)
		return true
	}
	handled, err := source.BindAdmittedNativePresentationWindow(ctx, r.URL.Query().Get("clientId"), r.URL.Query().Get("windowId"), key, &types.Window{Resource: selected.Resource})
	if err != nil {
		// Exact candidate admission already succeeded. A changed candidate cannot
		// replace an existing instance, but is not an authorization failure.
		if errors.Is(err, identity.ErrResourceStale) {
			http.Error(w, "report instance revision changed", http.StatusConflict)
		} else {
			writeWindowError(w, err)
		}
		return true
	}
	if !handled {
		writeWindowError(w, identity.ErrResourceDenied)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	// No definition or dataset is echoed through the registration response.
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "data": map[string]bool{"bound": true}})
	return true
}
