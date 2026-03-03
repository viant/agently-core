package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/memory"
	toolpolicy "github.com/viant/agently-core/protocol/tool"
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
	mux.HandleFunc("GET /v1/conversations", handleListConversations(client))

	mux.HandleFunc("GET /v1/messages", handleGetMessages(client))
	mux.HandleFunc("GET /v1/elicitations", handleListPendingElicitations(client))

	mux.HandleFunc("GET /v1/stream", handleStreamEvents(client))

	mux.HandleFunc("POST /v1/turns/{id}/cancel", handleCancelTurn(client))
	mux.HandleFunc("POST /v1/elicitations/{conversationId}/{elicitationId}/resolve", handleResolveElicitation(client))

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
		out, err := client.Query(r.Context(), &input)
		if err != nil {
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, out)
	}
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
			IncludeModelCalls: q.Get("includeModelCalls") == "true",
			IncludeToolCalls:  q.Get("includeToolCalls") == "true",
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
		input := &ListConversationsInput{
			AgentID: r.URL.Query().Get("agentId"),
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
			TurnID:         q.Get("turnId"),
		}
		if roles := q.Get("roles"); roles != "" {
			input.Roles = strings.Split(roles, ",")
		}
		if types := q.Get("types"); types != "" {
			input.Types = strings.Split(types, ",")
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
			httpError(w, http.StatusInternalServerError, err)
			return
		}
		httpJSON(w, http.StatusOK, map[string]interface{}{"rows": rows})
	}
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
