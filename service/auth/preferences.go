package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/viant/authz"
	"github.com/viant/xdatly/response"
	"net/http"
	"strings"

	iauth "github.com/viant/agently-core/internal/auth"
	identity "github.com/viant/agently-core/protocol/resource"
	primitive "github.com/viant/forge/backend/provider/contract"
)

var errPreferencesUnavailable = errors.New("user preferences are unavailable")

const preferencesChangedMessage = "Your settings changed. Please reload and retry."

type preferencesChangedError struct{ cause error }

func (e *preferencesChangedError) Error() string       { return preferencesChangedMessage }
func (e *preferencesChangedError) Unwrap() error       { return e.cause }
func (e *preferencesChangedError) HTTPStatusCode() int { return http.StatusConflict }

// PreferencesPatch describes a partial update to user preferences.
type PreferencesPatch struct {
	// ExpectedOwnerID binds a form to the account that supplied its initial data.
	ExpectedOwnerID *string `json:"expectedOwnerId,omitempty"`
	// These describe this write even if another client changes the row before
	// the tool responds. They are set only after the version-checked write.
	committedWorkspace     *WorkspacePreferences
	committedDefaultChange *WorkspaceDefaultChange
	WorkspaceAction        *WorkspaceMutation                `json:"workspaceAction,omitempty"`
	Workspace              *WorkspacePreferences             `json:"workspace,omitempty"`
	StarterTasks           *StarterTaskPreferences           `json:"starterTasks,omitempty"`
	DisplayName            *string                           `json:"displayName,omitempty"`
	Timezone               *string                           `json:"timezone,omitempty"`
	DefaultAgentRef        *string                           `json:"defaultAgentRef,omitempty"`
	DefaultModelRef        *string                           `json:"defaultModelRef,omitempty"`
	DefaultEmbedderRef     *string                           `json:"defaultEmbedderRef,omitempty"`
	AgentPrefs             map[string]map[string]interface{} `json:"agentPrefs,omitempty"`
}

// StarterTaskPreferences stores presentation choices and personally authored
// prompts. Published prompt bodies are always read from current workspace metadata.
type StarterTaskPreferences struct {
	// OwnerID accompanies the metadata read. It is never persisted or decoded from input.
	OwnerID string                  `json:"-"`
	Hidden  []string                `json:"hidden,omitempty"`
	Order   []string                `json:"order,omitempty"`
	Own     []primitive.StarterTask `json:"own,omitempty"`
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
		owner, err := preferenceUser(r.Context(), h.users, firstNonEmpty(iauth.CanonicalUserID(r.Context()), ui.Subject))
		if err != nil {
			writePreferenceError(w, err)
			return
		}
		preferences := map[string]any{}
		if owner != nil {
			for key, value := range owner.Preferences {
				preferences[key] = value
			}
			preferences["preferenceOwnerId"] = owner.ID
		}
		w.Header().Set("Cache-Control", "private, no-store")
		httpJSON(w, http.StatusOK, preferences)
	}
}

// preferenceUser binds the addressed preference record to the identity
// already verified by ingress, using the existing native user reader.
func preferenceUser(ctx context.Context, users UserService, username string) (*User, error) {
	if users == nil {
		return nil, errPreferencesUnavailable
	}
	canonical := iauth.CanonicalUserID(ctx)
	actor := iauth.User(ctx)
	provider := iauth.Provider(ctx)
	var owner *User
	var err error
	switch {
	case canonical != "":
		owner, err = users.GetByUsername(ctx, canonical)
	case actor != nil && actor.Subject != "" && provider != "":
		owner, err = users.GetBySubjectAndProvider(ctx, actor.Subject, provider)
	case actor != nil && actor.Subject != "":
		owner, err = users.GetByUsername(ctx, actor.Subject)
	default:
		return users.GetByUsername(ctx, username)
	}
	if err != nil || owner == nil {
		return nil, err
	}
	if canonical != "" && owner.ID != canonical {
		return nil, authz.ErrDenied
	}
	if canonical == "" && owner.Subject != actor.Subject {
		return nil, authz.ErrDenied
	}
	if username != owner.ID && username != owner.Username && (actor == nil || username != actor.Subject) {
		return nil, authz.ErrDenied
	}
	return owner, nil
}

func readPreferences(ctx context.Context, users UserService, username string) (map[string]any, error) {
	user, err := preferenceUser(ctx, users, username)
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
	owner, err := preferenceUser(ctx, r.ext.users, firstNonEmpty(iauth.CanonicalUserID(ctx), user.Subject))
	if err != nil || owner == nil {
		return nil, err
	}
	result := StarterTaskPreferences{OwnerID: owner.ID}
	if owner.Preferences["starterTasks"] == nil {
		return &result, nil
	}
	raw, err := json.Marshal(owner.Preferences["starterTasks"])
	if err != nil {
		return nil, err
	}
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
			httpError(w, http.StatusServiceUnavailable, errPreferencesUnavailable)
			return
		}
		var patch PreferencesPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := patch.Workspace.validate(); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := patch.StarterTasks.validate(); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := h.users.UpdatePreferences(r.Context(), firstNonEmpty(iauth.CanonicalUserID(r.Context()), ui.Subject), &patch); err != nil {
			writePreferenceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// The caller has already authenticated by this point. Errors from the store
// or report verifier may contain backend details that do not belong in HTTP.
func writePreferenceError(w http.ResponseWriter, err error) {
	status := response.ErrorStatusCode(err, http.StatusInternalServerError)
	var upstream interface{ HTTPStatusCode() int }
	var invalid *WorkspacePreferenceError
	switch {
	case errors.Is(err, context.Canceled):
		httpError(w, 499, errors.New("request canceled"))
		return
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, authz.ErrDenied):
		status = http.StatusForbidden
	case errors.Is(err, errPreferencesUnavailable):
		status = http.StatusServiceUnavailable
	case errors.As(err, &invalid):
		httpError(w, http.StatusBadRequest, invalid)
		return
	case errors.As(err, &upstream):
		status = upstream.HTTPStatusCode()
	case errors.Is(err, identity.ErrResourceDenied):
		status = http.StatusForbidden
	case errors.Is(err, identity.ErrResourceStale):
		status = http.StatusConflict
	}
	if status < 400 || status > 599 {
		status = http.StatusInternalServerError
	}
	if status == http.StatusUnauthorized {
		httpError(w, http.StatusBadGateway, errors.New("the preference service could not authenticate with its provider"))
		return
	}
	if status == http.StatusConflict {
		httpError(w, status, errors.New(preferencesChangedMessage))
		return
	}
	message := http.StatusText(status)
	if message == "" {
		message = "preference request failed"
	}
	httpError(w, status, errors.New(message))
}
