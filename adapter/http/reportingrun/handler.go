package reportingrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	convstore "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	reportingrunsvc "github.com/viant/agently-core/service/reportingrun"
)

const routePrefix = "/v1/api/report-runs"

// Handler is the authenticated, host-only browser run lifecycle boundary. It
// is mounted outside the tool registry, so it cannot become model-visible.
type Handler struct {
	service         *reportingrunsvc.Service
	conversations   convstore.Client
	adoptionEnabled bool
}

func New(service *reportingrunsvc.Service, conversations convstore.Client, adoptionEnabled bool) *Handler {
	return &Handler{service: service, conversations: conversations, adoptionEnabled: adoptionEnabled}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil {
		http.NotFound(w, r)
		return
	}
	if strings.TrimSpace(authctx.EffectiveUserID(r.Context())) == "" {
		writeError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, routePrefix), "/")
	if r.Method == http.MethodPost && path == "begin" {
		h.begin(w, r)
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(path, "context/") {
		conversationID, _ := url.PathUnescape(strings.TrimPrefix(path, "context/"))
		h.getContext(w, r, conversationID)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 && r.Method == http.MethodGet && strings.TrimSpace(parts[0]) != "" {
		h.getRun(w, r, parts[0])
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	reportRunID, _ := url.PathUnescape(parts[0])
	switch parts[1] {
	case "complete":
		h.complete(w, r, reportRunID)
	case "fail":
		h.fail(w, r, reportRunID)
	case "activate":
		h.activate(w, r, reportRunID)
	case "adopt":
		if !h.adoptionEnabled {
			http.NotFound(w, r)
			return
		}
		h.adopt(w, r, reportRunID)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) begin(w http.ResponseWriter, r *http.Request) {
	input := &reportingrunsvc.BeginInput{}
	if !decode(w, r, input) {
		return
	}
	if !h.validateConversation(w, r, input.ConversationID) {
		return
	}
	result, err := h.service.Begin(r.Context(), input)
	writeResult(w, result, err)
}

func (h *Handler) complete(w http.ResponseWriter, r *http.Request, reportRunID string) {
	input := &reportingrunsvc.CompleteInput{}
	if !decode(w, r, input) {
		return
	}
	input.ReportRunID = strings.TrimSpace(reportRunID)
	if !h.validateConversation(w, r, input.ConversationID) {
		return
	}
	result, err := h.service.Complete(r.Context(), input)
	writeResult(w, result, err)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, reportRunID string) {
	input := &reportingrunsvc.FailInput{}
	if !decode(w, r, input) {
		return
	}
	input.ReportRunID = strings.TrimSpace(reportRunID)
	if !h.validateConversation(w, r, input.ConversationID) {
		return
	}
	result, err := h.service.Fail(r.Context(), input)
	writeResult(w, result, err)
}

func (h *Handler) activate(w http.ResponseWriter, r *http.Request, reportRunID string) {
	input := &reportingrunsvc.ActivateInput{}
	if !decode(w, r, input) {
		return
	}
	input.ReportRunID = strings.TrimSpace(reportRunID)
	if !h.validateConversation(w, r, input.ConversationID) {
		return
	}
	result, err := h.service.Activate(r.Context(), input)
	writeResult(w, result, err)
}

func (h *Handler) adopt(w http.ResponseWriter, r *http.Request, reportRunID string) {
	input := &reportingrunsvc.AdoptInput{}
	if !decode(w, r, input) {
		return
	}
	input.ReportRunID = strings.TrimSpace(reportRunID)
	if !h.validateConversation(w, r, input.ConversationID) {
		return
	}
	result, err := h.service.Adopt(r.Context(), input)
	writeResult(w, result, err)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request, reportRunID string) {
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
	if !h.validateConversation(w, r, conversationID) {
		return
	}
	result, err := h.service.GetRun(r.Context(), reportRunID, conversationID)
	writeResult(w, result, err)
}

func (h *Handler) getContext(w http.ResponseWriter, r *http.Request, conversationID string) {
	if !h.validateConversation(w, r, conversationID) {
		return
	}
	result, err := h.service.GetContext(r.Context(), conversationID)
	writeResult(w, result, err)
}

func (h *Handler) validateConversation(w http.ResponseWriter, r *http.Request, conversationID string) bool {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return true
	}
	if h.conversations == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("conversation authorization is unavailable"))
		return false
	}
	conversation, err := h.conversations.GetConversation(r.Context(), conversationID)
	if err != nil || conversation == nil {
		writeError(w, http.StatusNotFound, reportingrunsvc.ErrNotFound)
		return false
	}
	ownerID := strings.TrimSpace(authctx.EffectiveUserID(r.Context()))
	if ownerID == "" || conversation.CreatedByUserId == nil ||
		strings.TrimSpace(*conversation.CreatedByUserId) != ownerID {
		writeError(w, http.StatusNotFound, reportingrunsvc.ErrNotFound)
		return false
	}
	return true
}

func decode(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("request body is required"))
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, result interface{}, err error) {
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, reportingrunsvc.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, reportingrunsvc.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, reportingrunsvc.ErrConflict), errors.Is(err, reportingrunsvc.ErrCAS):
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
