package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	primitive "github.com/viant/forge/backend/provider/contract"
)

// PreferencesPatch describes a partial update to user preferences.
type PreferencesPatch struct {
	StarterTasks       *StarterTaskPreferences           `json:"starterTasks,omitempty"`
	DisplayName        *string                           `json:"displayName,omitempty"`
	Timezone           *string                           `json:"timezone,omitempty"`
	DefaultAgentRef    *string                           `json:"defaultAgentRef,omitempty"`
	DefaultModelRef    *string                           `json:"defaultModelRef,omitempty"`
	DefaultEmbedderRef *string                           `json:"defaultEmbedderRef,omitempty"`
	AgentPrefs         map[string]map[string]interface{} `json:"agentPrefs,omitempty"`
}

// StarterTaskPreferences stores presentation choices and personally authored
// prompts. Published prompt bodies are always read from current workspace metadata.
type StarterTaskPreferences struct {
	Hidden []string                `json:"hidden,omitempty"`
	Order  []string                `json:"order,omitempty"`
	Own    []primitive.StarterTask `json:"own,omitempty"`
}

func (p *StarterTaskPreferences) validate() error {
	if p == nil {
		return nil
	}
	for _, ids := range [][]string{p.Hidden, p.Order} {
		if len(ids) > 1000 {
			return fmt.Errorf("too many starter task preferences")
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id == "" || len(id) > 512 || strings.TrimSpace(id) != id || seen[id] {
				return fmt.Errorf("invalid or duplicate starter task preference")
			}
			seen[id] = true
		}
	}
	if len(p.Own) > 100 {
		return fmt.Errorf("too many personal starter tasks")
	}
	seen := make(map[string]bool, len(p.Own))
	for _, task := range p.Own {
		if err := task.Validate(); err != nil {
			return err
		}
		if len(task.Title) > 255 || len(task.Prompt) > 16384 || len(task.Description) > 1024 || seen[task.ID] {
			return fmt.Errorf("invalid or duplicate personal starter task")
		}
		seen[task.ID] = true
	}
	return nil
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
		preferences, err := readPreferences(r.Context(), h.users, ui.Subject)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		httpJSON(w, http.StatusOK, preferences)
	}
}

func readPreferences(ctx context.Context, users UserService, username string) (map[string]any, error) {
	if users == nil {
		return map[string]any{}, nil
	}
	user, err := users.GetByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return map[string]any{}, nil
	}
	return user.Preferences, nil
}

// StarterTaskPreferences returns only the authenticated user's task preferences.
func (r *Runtime) StarterTaskPreferences(ctx context.Context) (*StarterTaskPreferences, error) {
	user := iauth.User(ctx)
	if r == nil || r.ext == nil || user == nil {
		return nil, nil
	}
	preferences, err := readPreferences(ctx, r.ext.users, user.Subject)
	if err != nil || preferences["starterTasks"] == nil {
		return nil, err
	}
	raw, err := json.Marshal(preferences["starterTasks"])
	if err != nil {
		return nil, err
	}
	var result StarterTaskPreferences
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, result.validate()
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
		if err := patch.StarterTasks.validate(); err != nil {
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
