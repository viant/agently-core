// Package callback provides an HTTP handler that dispatches foreground
// submit events (forge custom_callback) to workspace-declared tool
// invocations. Mount it at POST /v1/api/callbacks/dispatch via
// Handler.Register (see sdk.WithCallbackDispatchHandler).
//
// Authentication: the handler itself does not enforce auth. When mounted
// inside sdk.NewHandler, the outer svcauth.Protect middleware issues
// HTTP 401 for any /v1/ path when auth is configured and the caller is
// unauthenticated. The dispatch handler therefore only ever executes
// with either (a) auth disabled, or (b) an authenticated request — and
// the auth context flows into svc.Dispatch → tool invocation through
// r.Context() automatically.
package callback

import (
	"encoding/json"
	"net/http"
	"strings"

	callbacksvc "github.com/viant/agently-core/service/callback"
)

// dispatchPath is the HTTP route the dispatch endpoint mounts on.
const dispatchPath = "/v1/api/callbacks/dispatch"

// Handler mounts the callback dispatch endpoint on an HTTP mux.
type Handler struct {
	svc *callbacksvc.Service
}

// NewHandler builds a Handler around a dispatch service. svc may be nil;
// Register still mounts the route, but requests return 503 so callers
// degrade gracefully when no callback service is configured for the
// workspace.
func NewHandler(svc *callbacksvc.Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts the dispatch endpoint at POST /v1/api/callbacks/dispatch.
func (h *Handler) Register(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.Handle("POST "+dispatchPath, NewDispatchHandler(h.svc))
}

// NewDispatchHandler returns an http.Handler that accepts a JSON
// DispatchInput body and returns a JSON DispatchOutput. svc may be nil;
// requests then return 503.
//
// When auth is configured on the outer mux, unauthenticated requests
// are rejected with 401 BEFORE this handler runs (see svcauth.Protect).
// The handler therefore focuses only on request validation + dispatch
// + response encoding.
func NewDispatchHandler(svc *callbacksvc.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if svc == nil {
			http.Error(w, "callback dispatcher not configured", http.StatusServiceUnavailable)
			return
		}

		var in callbacksvc.DispatchInput
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// r.Context() carries the authenticated user (when auth is enabled)
		// via svcauth's runtime middleware. Dispatch + tool invocation see
		// that context transparently.
		out, err := svc.Dispatch(r.Context(), &in)
		if err != nil {
			// Message-based discrimination is intentional — the service
			// returns plain errors. "no callback registered" → 404 so the
			// client can fall back; everything else → 400.
			status := http.StatusBadRequest
			if strings.Contains(err.Error(), "no callback registered") {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(out); encErr != nil {
			// Response already started — log but don't try to rewrite the header.
			_ = encErr
		}
	})
}
