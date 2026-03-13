package sdk

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	convstore "github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
	"github.com/viant/agently-core/runtime/memory"
	svca2a "github.com/viant/agently-core/service/a2a"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/service/speech"
	"github.com/viant/agently-core/service/workflow"
	svcworkspace "github.com/viant/agently-core/service/workspace"
)

// HandlerOption customises the handler created by NewHandler.
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	authCfg          *iauth.Config
	authSessions     *svcauth.Manager
	authOpts         []svcauth.HandlerOption
	speechHandler    *speech.Handler
	schedulerHandler *scheduler.Handler
	schedulerSvc     *scheduler.Service // for watchdog lifecycle
	schedulerOpts    *SchedulerOptions  // controls API vs watchdog mode
	workflowHandler  *workflow.Handler
	metadataHandler  *svcworkspace.MetadataHandler
	fileBrowser      *svcworkspace.FileBrowserHandler
	a2aHandler       *svca2a.Handler
}

// SchedulerOptions controls scheduler behavior at the SDK level.
type SchedulerOptions struct {
	// EnableAPI mounts scheduler CRUD/run-now HTTP endpoints (default: true when handler is set).
	EnableAPI bool
	// EnableRunNow mounts the run-now endpoint (default: true when API is enabled).
	EnableRunNow bool
	// EnableWatchdog starts the background watchdog loop that polls for due schedules.
	EnableWatchdog bool
}

// WithAuth configures auth endpoints and middleware on the handler.
func WithAuth(cfg *iauth.Config, sessions *svcauth.Manager, opts ...svcauth.HandlerOption) HandlerOption {
	return func(c *handlerConfig) {
		c.authCfg = cfg
		c.authSessions = sessions
		c.authOpts = opts
	}
}

// WithSpeechHandler adds speech transcription endpoints.
func WithSpeechHandler(h *speech.Handler) HandlerOption {
	return func(c *handlerConfig) { c.speechHandler = h }
}

// WithSchedulerHandler adds scheduler CRUD endpoints.
func WithSchedulerHandler(h *scheduler.Handler) HandlerOption {
	return func(c *handlerConfig) { c.schedulerHandler = h }
}

// WithScheduler configures the scheduler with full control over API and watchdog modes.
// This is the recommended option for multi-pod deployments where you need to separate
// scheduler runners from API-only instances.
//
// Example — API-only pod (no watchdog):
//
//	sdk.WithScheduler(svc, handler, &sdk.SchedulerOptions{
//	    EnableAPI: true, EnableWatchdog: false,
//	})
//
// Example — Dedicated scheduler pod (watchdog only, no API):
//
//	sdk.WithScheduler(svc, nil, &sdk.SchedulerOptions{
//	    EnableWatchdog: true,
//	})
func WithScheduler(svc *scheduler.Service, handler *scheduler.Handler, opts *SchedulerOptions) HandlerOption {
	return func(c *handlerConfig) {
		c.schedulerSvc = svc
		c.schedulerHandler = handler
		c.schedulerOpts = opts
	}
}

// WithWorkflowHandler adds workflow run endpoints.
func WithWorkflowHandler(h *workflow.Handler) HandlerOption {
	return func(c *handlerConfig) { c.workflowHandler = h }
}

// WithMetadataHandler adds workspace metadata endpoints.
func WithMetadataHandler(h *svcworkspace.MetadataHandler) HandlerOption {
	return func(c *handlerConfig) { c.metadataHandler = h }
}

// WithFileBrowser adds file browser endpoints.
func WithFileBrowser(h *svcworkspace.FileBrowserHandler) HandlerOption {
	return func(c *handlerConfig) { c.fileBrowser = h }
}

// WithA2AHandler adds A2A protocol endpoints.
func WithA2AHandler(h *svca2a.Handler) HandlerOption {
	return func(c *handlerConfig) { c.a2aHandler = h }
}

// NewHandler creates an http.Handler that routes HTTP endpoints to the given Client.
// Any host application can mount this handler to expose the unified API.
// For scheduler watchdog support, use NewHandlerWithContext instead.
func NewHandler(client Client, opts ...HandlerOption) http.Handler {
	h, _ := NewHandlerWithContext(context.Background(), client, opts...)
	return h
}

// NewHandlerWithContext creates an http.Handler and starts any background services
// (e.g. scheduler watchdog) tied to the given context. When ctx is cancelled,
// background goroutines stop. Returns the handler and any startup error.
func NewHandlerWithContext(ctx context.Context, client Client, opts ...HandlerOption) (http.Handler, error) {
	cfg := &handlerConfig{}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}

	// Start scheduler watchdog if configured.
	if cfg.schedulerOpts != nil && cfg.schedulerOpts.EnableWatchdog && cfg.schedulerSvc != nil {
		go cfg.schedulerSvc.StartWatchdog(ctx)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth())

	mux.HandleFunc("POST /v1/agent/query", handleQuery(client))

	mux.HandleFunc("POST /v1/conversations", handleCreateConversation(client))
	mux.HandleFunc("GET /v1/conversations/{id}", handleGetConversation(client))
	mux.HandleFunc("PATCH /v1/conversations/{id}", handleUpdateConversation(client))
	mux.HandleFunc("GET /v1/conversations", handleListConversations(client))

	mux.HandleFunc("GET /v1/messages", handleGetMessages(client))
	mux.HandleFunc("GET /v1/elicitations", handleListPendingElicitations(client))
	mux.HandleFunc("GET /v1/api/payload/{id}", handleGetPayload(client))
	mux.HandleFunc("GET /v1/files", handleListFiles(client))
	mux.HandleFunc("GET /v1/files/{id}", handleDownloadFile(client))

	mux.HandleFunc("GET /v1/stream", handleStreamEvents(client))

	mux.HandleFunc("POST /v1/turns/{id}/cancel", handleCancelTurn(client))
	mux.HandleFunc("POST /v1/elicitations/{conversationId}/{elicitationId}/resolve", handleResolveElicitation(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/steer", handleSteerTurn(client))
	mux.HandleFunc("DELETE /v1/conversations/{id}/turns/{turnId}", handleDeleteQueuedTurn(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/move", handleMoveQueuedTurn(client))
	mux.HandleFunc("PATCH /v1/conversations/{id}/turns/{turnId}", handleEditQueuedTurn(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/force-steer", handleForceSteerQueuedTurn(client))

	mux.HandleFunc("POST /v1/tools/{name}/execute", handleExecuteTool(client))
	mux.HandleFunc("POST /v1/tools/execute", handleExecuteToolByName(client))
	mux.HandleFunc("GET /v1/tool-approvals/pending", handleListPendingToolApprovals(client))
	mux.HandleFunc("POST /v1/tool-approvals/{id}/decision", handleDecideToolApproval(client))

	mux.HandleFunc("POST /v1/workspace/resources/export", handleExportResources(client))
	mux.HandleFunc("POST /v1/workspace/resources/import", handleImportResources(client))
	mux.HandleFunc("GET /v1/workspace/resources/{kind}/{name}", handleGetResource(client))
	mux.HandleFunc("PUT /v1/workspace/resources/{kind}/{name}", handleSaveResource(client))
	mux.HandleFunc("DELETE /v1/workspace/resources/{kind}/{name}", handleDeleteResource(client))
	mux.HandleFunc("GET /v1/workspace/resources", handleListResources(client))

	mux.HandleFunc("GET /v1/conversations/{id}/transcript", handleGetTranscript(client))

	// Conversation maintenance
	mux.HandleFunc("POST /v1/conversations/{id}/terminate", handleTerminate(client))
	mux.HandleFunc("POST /v1/conversations/{id}/compact", handleCompact(client))
	mux.HandleFunc("POST /v1/conversations/{id}/prune", handlePrune(client))

	// Mount optional sub-handlers
	if cfg.authCfg != nil && cfg.authSessions != nil {
		ah := svcauth.NewHandler(cfg.authCfg, cfg.authSessions, cfg.authOpts...)
		ah.Register(mux)
		ah.RegisterPreferences(mux)
	}
	if cfg.speechHandler != nil {
		cfg.speechHandler.Register(mux)
	}
	// Mount scheduler endpoints based on options.
	if cfg.schedulerOpts != nil {
		if cfg.schedulerOpts.EnableAPI && cfg.schedulerHandler != nil {
			if cfg.schedulerOpts.EnableRunNow {
				cfg.schedulerHandler.Register(mux)
			} else {
				cfg.schedulerHandler.RegisterWithoutRunNow(mux)
			}
		}
	} else if cfg.schedulerHandler != nil {
		// Legacy: WithSchedulerHandler without WithScheduler — mount all endpoints.
		cfg.schedulerHandler.Register(mux)
	}
	if cfg.workflowHandler != nil {
		cfg.workflowHandler.Register(mux)
	}
	if cfg.metadataHandler != nil {
		cfg.metadataHandler.Register(mux)
	}
	if cfg.fileBrowser != nil {
		cfg.fileBrowser.Register(mux)
	}
	if cfg.a2aHandler != nil {
		cfg.a2aHandler.Register(mux)
	}

	// Apply auth middleware if configured
	var handler http.Handler = mux
	if cfg.authCfg != nil && cfg.authCfg.Enabled && cfg.authSessions != nil {
		handler = svcauth.Protect(cfg.authCfg, cfg.authSessions)(mux)
	}
	return handler, nil
}

type payloadReader interface {
	GetPayload(ctx context.Context, id string) (*convstore.Payload, error)
}

func handleListFiles(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		if conversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.ListFiles(r.Context(), &ListFilesInput{ConversationID: conversationID})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleDownloadFile(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		fileID := strings.TrimSpace(r.PathValue("id"))
		if conversationID == "" || fileID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and file ID are required"))
			return
		}
		out, err := client.DownloadFile(r.Context(), &DownloadFileInput{
			ConversationID: conversationID,
			FileID:         fileID,
		})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("file not found"))
			return
		}
		if queryBool(r, "raw", false) {
			contentType := strings.TrimSpace(out.ContentType)
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			if name := strings.TrimSpace(out.Name); name != "" {
				w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out.Data)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetPayload(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("payload ID is required"))
			return
		}
		reader, ok := client.(payloadReader)
		if !ok {
			httpError(w, http.StatusNotImplemented, fmt.Errorf("payload endpoint is unavailable for this client mode"))
			return
		}
		payload, err := reader.GetPayload(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if payload == nil {
			httpError(w, http.StatusNotFound, fmt.Errorf("payload not found"))
			return
		}

		rawMode := queryBool(r, "raw", false)
		metaMode := queryBool(r, "meta", false)
		inlineMode := queryBool(r, "inline", true)

		body := payloadBytes(payload)
		compression := strings.TrimSpace(payload.Compression)
		if strings.EqualFold(compression, "gzip") && len(body) > 0 {
			if inflated, ok := inflateGZIP(body); ok {
				body = inflated
				compression = ""
			}
		}

		if rawMode {
			if len(body) == 0 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			contentType := strings.TrimSpace(payload.MimeType)
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			w.Header().Set("Content-Type", contentType)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
			return
		}

		out := *payload
		out.Compression = compression
		if metaMode || !inlineMode {
			out.InlineBody = nil
		} else {
			copied := append([]byte(nil), body...)
			out.InlineBody = &copied
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func payloadBytes(p *convstore.Payload) []byte {
	if p == nil || p.InlineBody == nil {
		return nil
	}
	return append([]byte(nil), (*p.InlineBody)...)
}

func inflateGZIP(data []byte) ([]byte, bool) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	defer reader.Close()
	var out bytes.Buffer
	if _, err = io.Copy(&out, reader); err != nil {
		return nil, false
	}
	return out.Bytes(), true
}

func queryBool(r *http.Request, key string, fallback bool) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		httpJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleQuery(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input agentsvc.QueryInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.UserId = resolveQueryUserID(w, r, input.UserId)
		out, err := client.Query(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

const anonymousUserCookieName = "agently_anonymous_user"

func resolveQueryUserID(w http.ResponseWriter, r *http.Request, explicit string) string {
	userID := strings.TrimSpace(explicit)
	if userID != "" {
		return userID
	}
	if derived := strings.TrimSpace(iauth.EffectiveUserID(r.Context())); derived != "" {
		return derived
	}
	if cookie, err := r.Cookie(anonymousUserCookieName); err == nil {
		if existing := strings.TrimSpace(cookie.Value); existing != "" {
			return existing
		}
	}
	anonymousID := "anonymous:" + uuid.NewString()
	http.SetCookie(w, &http.Cookie{
		Name:     anonymousUserCookieName,
		Value:    anonymousID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
	return anonymousID
}

func handleCreateConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input CreateConversationInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.CreateConversation(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		out, err := client.GetConversation(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleUpdateConversation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		var body struct {
			Visibility string `json:"visibility"`
			Shareable  *bool  `json:"shareable"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input := &UpdateConversationInput{
			ConversationID: id,
			Visibility:     strings.TrimSpace(body.Visibility),
			Shareable:      body.Shareable,
		}
		out, err := client.UpdateConversation(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetTranscript(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		q := r.URL.Query()
		input := &GetTranscriptInput{
			ConversationID:    id,
			Since:             q.Get("since"),
			IncludeModelCalls: q.Get("includeModelCalls") == "true" || q.Get("includeModelCall") == "true",
			IncludeToolCalls:  q.Get("includeToolCalls") == "true" || q.Get("includeToolCall") == "true",
		}
		out, err := client.GetTranscript(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleListConversations(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		input := &ListConversationsInput{
			AgentID: strings.TrimSpace(q.Get("agentId")),
			Query:   strings.TrimSpace(q.Get("q")),
			Status:  strings.TrimSpace(q.Get("status")),
		}
		if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
			limit, err := strconv.Atoi(limitRaw)
			if err != nil || limit <= 0 {
				httpError(w, http.StatusBadRequest, fmt.Errorf("invalid limit"))
				return
			}
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Limit = limit
		}
		if cursor := strings.TrimSpace(q.Get("cursor")); cursor != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Cursor = cursor
		}
		if direction := strings.TrimSpace(q.Get("direction")); direction != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Direction = Direction(direction)
		}
		out, err := client.ListConversations(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetMessages(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		input := &GetMessagesInput{
			ConversationID: q.Get("conversationId"),
			ID:             q.Get("id"),
			TurnID:         q.Get("turnId"),
		}
		if roles := q.Get("roles"); roles != "" {
			input.Roles = strings.Split(roles, ",")
		}
		if types := q.Get("types"); types != "" {
			input.Types = strings.Split(types, ",")
		}
		if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
			limit, err := strconv.Atoi(limitRaw)
			if err != nil || limit <= 0 {
				httpError(w, http.StatusBadRequest, fmt.Errorf("invalid limit"))
				return
			}
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Limit = limit
		}
		if cursor := strings.TrimSpace(q.Get("cursor")); cursor != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Cursor = cursor
		}
		if direction := strings.TrimSpace(q.Get("direction")); direction != "" {
			if input.Page == nil {
				input.Page = &PageInput{}
			}
			input.Page.Direction = Direction(direction)
		}
		out, err := client.GetMessages(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleListPendingElicitations(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.URL.Query().Get("conversationId"))
		if conversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		rows, err := client.ListPendingElicitations(r.Context(), &ListPendingElicitationsInput{
			ConversationID: conversationID,
		})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"rows": rows})
	}
}

func handleStreamEvents(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		convID := r.URL.Query().Get("conversationId")
		input := &StreamEventsInput{ConversationID: convID}
		sub, err := client.StreamEvents(r.Context(), input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		defer sub.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}

		ctx := r.Context()
		for {
			select {
			case ev, open := <-sub.C():
				if !open {
					return
				}
				data, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data:%s\n\n", data)
				if ok {
					flusher.Flush()
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

func handleCancelTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		cancelled, err := client.CancelTurn(r.Context(), id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]bool{"cancelled": cancelled})
	}
}

func handleSteerTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		turnID := strings.TrimSpace(r.PathValue("turnId"))
		if conversationID == "" || turnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and turn ID are required"))
			return
		}
		var input SteerTurnInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		out, err := client.SteerTurn(r.Context(), &input)
		if err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			out = &SteerTurnOutput{TurnID: turnID, Status: "accepted"}
		}
		httpJSON(w, http.StatusAccepted, out)
	}
}

func handleDeleteQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		turnID := strings.TrimSpace(r.PathValue("turnId"))
		if conversationID == "" || turnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and turn ID are required"))
			return
		}
		if err := client.CancelQueuedTurn(r.Context(), conversationID, turnID); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleMoveQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		turnID := strings.TrimSpace(r.PathValue("turnId"))
		if conversationID == "" || turnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and turn ID are required"))
			return
		}
		var input MoveQueuedTurnInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		if err := client.MoveQueuedTurn(r.Context(), &input); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleEditQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		turnID := strings.TrimSpace(r.PathValue("turnId"))
		if conversationID == "" || turnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and turn ID are required"))
			return
		}
		var input EditQueuedTurnInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		input.ConversationID = conversationID
		input.TurnID = turnID
		if err := client.EditQueuedTurn(r.Context(), &input); err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func handleForceSteerQueuedTurn(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("id"))
		turnID := strings.TrimSpace(r.PathValue("turnId"))
		if conversationID == "" || turnID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID and turn ID are required"))
			return
		}
		out, err := client.ForceSteerQueuedTurn(r.Context(), conversationID, turnID)
		if err != nil {
			if isConflictError(err) {
				httpError(w, http.StatusConflict, err)
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		if out == nil {
			out = &SteerTurnOutput{TurnID: turnID, Status: "accepted"}
		}
		httpJSON(w, http.StatusAccepted, out)
	}
}

func handleResolveElicitation(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conversationID := strings.TrimSpace(r.PathValue("conversationId"))
		elicitationID := strings.TrimSpace(r.PathValue("elicitationId"))
		if conversationID == "" || elicitationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversationId and elicitationId are required"))
			return
		}
		var body struct {
			Action  string                 `json:"action"`
			Payload map[string]interface{} `json:"payload"`
		}
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		in := &ResolveElicitationInput{
			ConversationID: conversationID,
			ElicitationID:  elicitationID,
			Action:         strings.TrimSpace(body.Action),
			Payload:        body.Payload,
		}
		if in.Action == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("action is required"))
			return
		}
		if err := client.ResolveElicitation(r.Context(), in); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

func handleExecuteTool(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		var args map[string]interface{}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&args)
		}
		ctx := r.Context()
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = memory.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		result, err := client.ExecuteTool(ctx, name, args)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
	}
}

func handleExecuteToolByName(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string                 `json:"name"`
			Args map[string]interface{} `json:"args"`
		}
		if err := decodeJSON(r, &req); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("tool name is required"))
			return
		}
		ctx := r.Context()
		if convID := strings.TrimSpace(r.URL.Query().Get("conversationId")); convID != "" {
			ctx = memory.WithConversationID(ctx, convID)
		}
		ctx = ensureDirectToolPolicy(ctx)
		result, err := client.ExecuteTool(ctx, name, req.Args)
		if err != nil {
			httpError(w, statusForToolExecuteError(err), err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]string{"result": result})
	}
}

func handleListPendingToolApprovals(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		rows, err := client.ListPendingToolApprovals(r.Context(), &ListPendingToolApprovalsInput{
			UserID:         strings.TrimSpace(q.Get("userId")),
			ConversationID: strings.TrimSpace(q.Get("conversationId")),
			Status:         strings.TrimSpace(q.Get("status")),
		})
		if err != nil {
			if isToolApprovalQueueNotConfiguredErr(err) {
				// Queue support is optional; keep UI poll healthy when queue is not configured.
				httpJSON(w, http.StatusOK, map[string]interface{}{
					"data": []interface{}{},
				})
				return
			}
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{
			"data": rows,
		})
	}
}

func isToolApprovalQueueNotConfiguredErr(err error) bool {
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	return strings.Contains(msg, "tool approval queue not configured")
}

func handleDecideToolApproval(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("approval id is required"))
			return
		}
		var body DecideToolApprovalInput
		if err := decodeJSON(r, &body); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		body.ID = id
		out, err := client.DecideToolApproval(r.Context(), &body)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func statusForToolExecuteError(err error) int {
	if toolpolicy.IsPolicyError(err) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func ensureDirectToolPolicy(ctx context.Context) context.Context {
	if toolpolicy.FromContext(ctx) != nil {
		return ctx
	}
	// Direct tool execution endpoints should default to best_path safety mode.
	return toolpolicy.WithPolicy(ctx, &toolpolicy.Policy{Mode: toolpolicy.ModeBestPath})
}

func handleListResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		out, err := client.ListResources(r.Context(), &ListResourcesInput{Kind: kind})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleGetResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		out, err := client.GetResource(r.Context(), &ResourceRef{Kind: kind, Name: name})
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleSaveResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		if err := client.SaveResource(r.Context(), &SaveResourceInput{Kind: kind, Name: name, Data: body}); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteResource(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := r.PathValue("kind")
		name := r.PathValue("name")
		if err := client.DeleteResource(r.Context(), &ResourceRef{Kind: kind, Name: name}); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleExportResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input ExportResourcesInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.ExportResources(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func handleTerminate(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.TerminateConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCompact(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.CompactConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handlePrune(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if err := client.PruneConversation(r.Context(), id); err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleImportResources(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input ImportResourcesInput
		if err := decodeJSON(r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		out, err := client.ImportResources(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
}

func decodeJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return fmt.Errorf("request body is empty")
	}
	return json.NewDecoder(r.Body).Decode(v)
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

// WaitForReady blocks until the server at baseURL responds or timeout expires.
func WaitForReady(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/conversations")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v", timeout)
}
