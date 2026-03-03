package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Handler serves A2A protocol endpoints on the SDK's shared mux.
// This is for the "embedded" A2A access (via the SDK handler),
// as opposed to the per-agent servers launched by StartServers.
type Handler struct {
	svc *Service
}

// NewHandler creates an A2A HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// Register mounts A2A routes on the given mux.
func (h *Handler) Register(mux *http.ServeMux) {
	// Agent card.
	mux.HandleFunc("GET /v1/api/a2a/agents/{agentId}/card", h.handleGetAgentCard())
	// Send message (synchronous).
	mux.HandleFunc("POST /v1/api/a2a/agents/{agentId}/message", h.handleSendMessage())
	// List A2A-enabled agents.
	mux.HandleFunc("GET /v1/api/a2a/agents", h.handleListAgents())
	// Well-known agent card (per A2A spec, agentId as query param).
	mux.HandleFunc("GET /.well-known/agent.json", h.handleWellKnown())
}

func (h *Handler) handleGetAgentCard() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("agentId")
		if agentID == "" {
			respondError(w, http.StatusBadRequest, fmt.Errorf("agent ID is required"))
			return
		}
		card, err := h.svc.GetAgentCard(r.Context(), agentID)
		if err != nil {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondJSON(w, http.StatusOK, card)
	}
}

func (h *Handler) handleSendMessage() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.PathValue("agentId")
		if agentID == "" {
			respondError(w, http.StatusBadRequest, fmt.Errorf("agent ID is required"))
			return
		}
		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := h.svc.SendMessage(r.Context(), agentID, &req)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, resp)
	}
}

func (h *Handler) handleListAgents() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Caller provides agent IDs as comma-separated query param.
		// Without IDs, returns empty list — we don't enumerate all agents.
		idsParam := r.URL.Query().Get("ids")
		var ids []string
		if idsParam != "" {
			for _, id := range splitCSV(idsParam) {
				if id != "" {
					ids = append(ids, id)
				}
			}
		}
		result, err := h.svc.ListA2AAgents(r.Context(), ids)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"agents": result})
	}
}

func (h *Handler) handleWellKnown() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agentId")
		if agentID == "" {
			respondError(w, http.StatusBadRequest, fmt.Errorf("agentId query parameter is required"))
			return
		}
		card, err := h.svc.GetAgentCard(r.Context(), agentID)
		if err != nil {
			respondError(w, http.StatusNotFound, err)
			return
		}
		respondJSON(w, http.StatusOK, card)
	}
}

func respondJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func respondError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func splitCSV(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
