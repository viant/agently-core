package auth

import (
	"encoding/json"
	"fmt"
	"net/http"

	iauth "github.com/viant/agently-core/internal/auth"
)

// PreferencesPatch describes a partial update to user preferences.
type PreferencesPatch struct {
	DisplayName        *string                           `json:"displayName,omitempty"`
	Timezone           *string                           `json:"timezone,omitempty"`
	DefaultAgentRef    *string                           `json:"defaultAgentRef,omitempty"`
	DefaultModelRef    *string                           `json:"defaultModelRef,omitempty"`
	DefaultEmbedderRef *string                           `json:"defaultEmbedderRef,omitempty"`
	AgentPrefs         map[string]map[string]interface{} `json:"agentPrefs,omitempty"`
}

// RegisterPreferences mounts preference endpoints on the given mux.
// These endpoints require the auth middleware to populate user context.
func (h *Handler) RegisterPreferences(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/api/me/preferences", h.handleGetPreferences())
	mux.HandleFunc("PATCH /v1/api/me/preferences", h.handlePatchPreferences())
}

func (h *Handler) handleGetPreferences() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ui := iauth.User(r.Context())
		if ui == nil {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
			return
		}
		if h.users == nil {
			httpJSON(w, http.StatusOK, map[string]interface{}{})
			return
		}
		u, err := h.users.GetByUsername(r.Context(), ui.Subject)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if u == nil {
			httpJSON(w, http.StatusOK, map[string]interface{}{})
			return
		}
		httpJSON(w, http.StatusOK, u.Preferences)
	}
}

func (h *Handler) handlePatchPreferences() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ui := iauth.User(r.Context())
		if ui == nil {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("not authenticated"))
			return
		}
		if h.users == nil {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("user service not configured"))
			return
		}
		var patch PreferencesPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.users.UpdatePreferences(r.Context(), ui.Subject, &patch); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
