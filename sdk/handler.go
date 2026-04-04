package sdk

import (
	"context"
	"fmt"
	"log"
	"net/http"

	svca2a "github.com/viant/agently-core/service/a2a"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/service/speech"
	"github.com/viant/agently-core/service/workflow"
	svcworkspace "github.com/viant/agently-core/service/workspace"
)

// HandlerOption customises the handler created by NewHandler.
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	authCfg          *svcauth.Config
	authSessions     *svcauth.Manager
	authOpts         []svcauth.HandlerOption
	speechHandler    *speech.Handler
	schedulerHandler *scheduler.Handler
	schedulerSvc     *scheduler.Service
	schedulerOpts    *SchedulerOptions
	workflowHandler  *workflow.Handler
	metadataHandler  *svcworkspace.MetadataHandler
	fileBrowser      *svcworkspace.FileBrowserHandler
	a2aHandler       *svca2a.Handler
}

// SchedulerOptions controls scheduler behavior at the SDK level.
type SchedulerOptions struct {
	EnableAPI      bool
	EnableRunNow   bool
	EnableWatchdog bool
}

func WithAuth(cfg *svcauth.Config, sessions *svcauth.Manager, opts ...svcauth.HandlerOption) HandlerOption {
	return func(c *handlerConfig) {
		c.authCfg = cfg
		c.authSessions = sessions
		c.authOpts = opts
	}
}

func WithSpeechHandler(h *speech.Handler) HandlerOption {
	return func(c *handlerConfig) { c.speechHandler = h }
}

func WithSchedulerHandler(h *scheduler.Handler) HandlerOption {
	return func(c *handlerConfig) { c.schedulerHandler = h }
}

func WithScheduler(svc *scheduler.Service, handler *scheduler.Handler, opts *SchedulerOptions) HandlerOption {
	return func(c *handlerConfig) {
		c.schedulerSvc = svc
		c.schedulerHandler = handler
		c.schedulerOpts = opts
	}
}

func WithWorkflowHandler(h *workflow.Handler) HandlerOption {
	return func(c *handlerConfig) { c.workflowHandler = h }
}

func WithMetadataHandler(h *svcworkspace.MetadataHandler) HandlerOption {
	return func(c *handlerConfig) { c.metadataHandler = h }
}

func WithFileBrowser(h *svcworkspace.FileBrowserHandler) HandlerOption {
	return func(c *handlerConfig) { c.fileBrowser = h }
}

func WithA2AHandler(h *svca2a.Handler) HandlerOption {
	return func(c *handlerConfig) { c.a2aHandler = h }
}

func NewHandler(client Client, opts ...HandlerOption) http.Handler {
	h, err := NewHandlerWithContext(context.Background(), client, opts...)
	if err == nil {
		return h
	}
	log.Printf("[sdk] NewHandler failed: %v", err)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpError(w, http.StatusInternalServerError, fmt.Errorf("failed to initialize handler: %w", err))
	})
}

func NewHandlerWithContext(ctx context.Context, client Client, opts ...HandlerOption) (http.Handler, error) {
	cfg := &handlerConfig{}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}

	if cfg.schedulerOpts != nil && cfg.schedulerOpts.EnableWatchdog && cfg.schedulerSvc != nil {
		go cfg.schedulerSvc.StartWatchdog(ctx)
	}

	mux := http.NewServeMux()
	registerCoreRoutes(mux, client, cfg)
	registerOptionalRoutes(mux, cfg)

	var handler http.Handler = mux
	if cfg.authCfg != nil && cfg.authCfg.Enabled && cfg.authSessions != nil {
		handler = svcauth.Protect(cfg.authCfg, cfg.authSessions)(mux)
	}
	return handler, nil
}

func registerCoreRoutes(mux *http.ServeMux, client Client, cfg *handlerConfig) {
	mux.HandleFunc("GET /healthz", handleHealth())
	mux.HandleFunc("GET /health", handleHealth())

	mux.HandleFunc("POST /v1/agent/query", handleQuery(client, cfg.authCfg))

	mux.HandleFunc("POST /v1/conversations", handleCreateConversation(client))
	mux.HandleFunc("GET /v1/conversations/{id}", handleGetConversation(client))
	mux.HandleFunc("PATCH /v1/conversations/{id}", handleUpdateConversation(client))
	mux.HandleFunc("GET /v1/conversations", handleListConversations(client))
	mux.HandleFunc("GET /v1/conversations/linked", handleListLinkedConversations(client))
	mux.HandleFunc("GET /v1/conversations/{id}/transcript", handleGetTranscript(client))
	mux.HandleFunc("GET /v1/conversations/{id}/live-state", handleGetLiveState(client))
	mux.HandleFunc("POST /v1/conversations/{id}/terminate", handleTerminate(client))
	mux.HandleFunc("POST /v1/conversations/{id}/compact", handleCompact(client))
	mux.HandleFunc("POST /v1/conversations/{id}/prune", handlePrune(client))

	mux.HandleFunc("GET /v1/messages", handleGetMessages(client))
	mux.HandleFunc("GET /v1/elicitations", handleListPendingElicitations(client))
	mux.HandleFunc("GET /v1/api/payload/{id}", handleGetPayload(client))
	mux.HandleFunc("GET /v1/api/conversations/{id}/generated-files", handleListGeneratedFiles(client))
	mux.HandleFunc("GET /v1/api/generated-files/{id}/download", handleDownloadGeneratedFile(client))
	mux.HandleFunc("POST /v1/files", handleUploadFile(client))
	mux.HandleFunc("GET /v1/files", handleListFiles(client))
	mux.HandleFunc("GET /v1/files/{id}", handleDownloadFile(client))
	mux.HandleFunc("GET /v1/feeds", handleListFeeds(client))
	mux.HandleFunc("GET /v1/feeds/{id}/data", handleGetFeedData(client))
	mux.HandleFunc("GET /v1/stream", handleStreamEvents(client))

	mux.HandleFunc("POST /v1/turns/{id}/cancel", handleCancelTurn(client))
	mux.HandleFunc("POST /v1/elicitations/{conversationId}/{elicitationId}/resolve", handleResolveElicitation(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/steer", handleSteerTurn(client))
	mux.HandleFunc("DELETE /v1/conversations/{id}/turns/{turnId}", handleDeleteQueuedTurn(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/move", handleMoveQueuedTurn(client))
	mux.HandleFunc("PATCH /v1/conversations/{id}/turns/{turnId}", handleEditQueuedTurn(client))
	mux.HandleFunc("POST /v1/conversations/{id}/turns/{turnId}/force-steer", handleForceSteerQueuedTurn(client))

	mux.HandleFunc("GET /v1/tools", handleListToolDefinitions(client))
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
}

func registerOptionalRoutes(mux *http.ServeMux, cfg *handlerConfig) {
	if cfg.authCfg != nil && cfg.authSessions != nil {
		ah := svcauth.NewHandler(cfg.authCfg, cfg.authSessions, cfg.authOpts...)
		ah.Register(mux)
		ah.RegisterPreferences(mux)
	}
	if cfg.speechHandler != nil {
		cfg.speechHandler.Register(mux)
	}
	if cfg.schedulerOpts != nil {
		if cfg.schedulerOpts.EnableAPI && cfg.schedulerHandler != nil {
			if cfg.schedulerOpts.EnableRunNow {
				cfg.schedulerHandler.Register(mux)
			} else {
				cfg.schedulerHandler.RegisterWithoutRunNow(mux)
			}
		}
	} else if cfg.schedulerHandler != nil {
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
}
