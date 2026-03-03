package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// RunRequest describes a workflow run to execute.
type RunRequest struct {
	Location       string      `json:"location"`
	TaskID         string      `json:"taskId,omitempty"`
	Input          interface{} `json:"input,omitempty"`
	Title          string      `json:"title,omitempty"`
	TimeoutSeconds int         `json:"timeoutSeconds,omitempty"`
}

// RunResponse is the result of launching a workflow run.
type RunResponse struct {
	ConversationID string `json:"conversationId"`
}

// Handler serves workflow endpoints.
type Handler struct {
	agent *agentsvc.Service
}

// NewHandler creates a workflow HTTP handler.
func NewHandler(agent *agentsvc.Service) *Handler {
	return &Handler{agent: agent}
}

// Register mounts workflow routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/api/workflow/run", h.handleRun())
}

func (h *Handler) handleRun() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if req.Location == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("location is required"))
			return
		}
		convID := uuid.New().String()

		// Fire-and-forget: launch workflow in background goroutine.
		go func() {
			// Placeholder: full implementation would:
			// 1. Load workflow definition from req.Location
			// 2. Create a conversation with convID
			// 3. Execute workflow steps
			// 4. Record results
			_ = h.agent
		}()

		httpJSON(w, http.StatusOK, &RunResponse{ConversationID: convID})
	}
}

func httpJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
