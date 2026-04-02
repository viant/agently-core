package a2a

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	tokenpkg "github.com/viant/agently-core/internal/auth/token"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

// ServerConfig holds configuration for the A2A server launcher.
type ServerConfig struct {
	// AgentService is the agent query service.
	AgentService *agentsvc.Service
	// AgentFinder resolves agents by ID.
	AgentFinder agentmodel.Finder
	// AgentIDs is the list of agent IDs to check for A2A serving.
	AgentIDs []string
	// ApplyUserCred is an optional callback to inject user credentials
	// into the context for agents with UserCredURL configured.
	// Signature: func(ctx context.Context, credURL string) (context.Context, error)
	ApplyUserCred func(ctx context.Context, credURL string) (context.Context, error)
	// JWTService verifies inbound bearer tokens for protected A2A endpoints.
	JWTService *svcauth.JWTService
	// TokenProvider, when set, allows the A2A auth middleware to register
	// inbound tokens so mid-turn refresh works via EnsureTokens.
	TokenProvider tokenpkg.Provider
}

// A2AAuthConfig is a runtime-agnostic auth configuration for exposing an A2A endpoint.
type A2AAuthConfig struct {
	Enabled       bool
	Resource      string
	Scopes        []string
	UseIDToken    bool
	ExcludePrefix string
}

// A2AConfig is a runtime-agnostic serving configuration for one exposed agent.
type A2AConfig struct {
	Enabled     bool
	Port        int
	Streaming   bool
	UserCredURL string
}

// ExposedAgent describes one agent endpoint to expose over A2A.
type ExposedAgent struct {
	ID          string
	Name        string
	Description string
	A2A         *A2AConfig
	Auth        *A2AAuthConfig
}

// QueryResult is the minimal result needed to translate an agent query into an A2A task.
type QueryResult struct {
	MessageID      string
	ConversationID string
	Content        string
}

// GenericServerConfig allows non-core runtimes to reuse the core A2A launcher.
type GenericServerConfig struct {
	AgentIDs      []string
	ResolveAgent  func(ctx context.Context, agentID string) (*ExposedAgent, error)
	Query         func(ctx context.Context, agentID, query, conversationID string) (*QueryResult, error)
	ApplyUserCred func(ctx context.Context, credURL string) (context.Context, error)
	JWTService    *svcauth.JWTService
	// TokenProvider, when set, allows the A2A auth middleware to register
	// inbound tokens so mid-turn refresh works via EnsureTokens.
	TokenProvider tokenpkg.Provider
}

// StartServers iterates agents with A2A enabled and launches a dedicated
// HTTP server per agent on its configured port. Each server exposes:
//   - GET  /.well-known/agent-card.json — agent card discovery
//   - GET  /.well-known/oauth-protected-resource — OAuth metadata (if auth enabled)
//   - POST /v1/message:send — send message (JSON-RPC envelope or plain JSON)
//   - All routes wrapped with auth middleware when configured
//
// This mirrors agently's startA2AServers pattern.
func StartServers(ctx context.Context, cfg *ServerConfig) {
	if cfg == nil || cfg.AgentFinder == nil || cfg.AgentService == nil {
		return
	}

	// Validate one agent per port.
	portAgent := make(map[int]string)
	type agentEntry struct {
		agent *agentmodel.Agent
		a2a   *agentmodel.ServeA2A
	}
	var entries []agentEntry

	for _, id := range cfg.AgentIDs {
		ag, err := cfg.AgentFinder.Find(ctx, id)
		if err != nil {
			continue
		}
		a2aCfg := EffectiveA2A(ag)
		if a2aCfg == nil || !a2aCfg.Enabled || a2aCfg.Port <= 0 {
			continue
		}
		if existing, ok := portAgent[a2aCfg.Port]; ok {
			log.Printf("[a2a] port %d already claimed by agent %s, skipping %s", a2aCfg.Port, existing, id)
			continue
		}
		portAgent[a2aCfg.Port] = id
		entries = append(entries, agentEntry{agent: ag, a2a: a2aCfg})
	}

	for _, e := range entries {
		go startAgentServer(ctx, cfg, e.agent, e.a2a)
	}
}

// StartServersGeneric starts A2A servers using runtime-agnostic lookup/query callbacks.
func StartServersGeneric(ctx context.Context, cfg *GenericServerConfig) {
	if cfg == nil || cfg.ResolveAgent == nil || cfg.Query == nil {
		return
	}

	portAgent := make(map[int]string)
	type agentEntry struct {
		agent *ExposedAgent
	}
	var entries []agentEntry

	for _, id := range cfg.AgentIDs {
		ag, err := cfg.ResolveAgent(ctx, id)
		if err != nil || ag == nil || ag.A2A == nil || !ag.A2A.Enabled || ag.A2A.Port <= 0 {
			continue
		}
		if existing, ok := portAgent[ag.A2A.Port]; ok {
			log.Printf("[a2a] port %d already claimed by agent %s, skipping %s", ag.A2A.Port, existing, id)
			continue
		}
		portAgent[ag.A2A.Port] = id
		entries = append(entries, agentEntry{agent: ag})
	}

	for _, e := range entries {
		go startGenericAgentServer(ctx, cfg, e.agent)
	}
}

func startAgentServer(ctx context.Context, cfg *ServerConfig, ag *agentmodel.Agent, a2aCfg *agentmodel.ServeA2A) {
	name := ag.ID
	if name == "" {
		name = ag.Name
	}

	// Build agent card.
	card := AgentCard{
		Name:         name,
		Capabilities: &AgentCapabilities{Streaming: a2aCfg.Streaming},
	}
	if ag.Profile != nil {
		card.Description = ag.Profile.Description
	}
	if a2aCfg.Auth != nil && a2aCfg.Auth.Enabled {
		card.Authentication = map[string]interface{}{
			"type":       "bearer",
			"resource":   a2aCfg.Auth.Resource,
			"scopes":     a2aCfg.Auth.Scopes,
			"useIDToken": a2aCfg.Auth.UseIDToken,
		}
	}

	// Create the inner mux with A2A routes.
	inner := http.NewServeMux()

	// POST /v1/message:send — handles both JSON-RPC and plain JSON.
	inner.HandleFunc("POST /v1/message:send", handleMessageSend(cfg, ag, a2aCfg))

	// POST /v1/message:stream — SSE streaming endpoint.
	if a2aCfg.Streaming {
		inner.HandleFunc("POST /v1/message:stream", handleMessageStream(cfg, ag, a2aCfg))
	}

	// Build outer mux with well-known endpoints.
	outer := http.NewServeMux()

	// Agent card discovery.
	outer.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})

	// Wire auth if configured.
	if a2aCfg.Auth != nil && a2aCfg.Auth.Enabled {
		// Serve OAuth protected resource metadata.
		outer.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]interface{}{
				"resource": a2aCfg.Auth.Resource,
			}
			if len(a2aCfg.Auth.Scopes) > 0 {
				meta["scopes_supported"] = a2aCfg.Auth.Scopes
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(meta)
		})
		// Wrap inner routes with auth middleware.
		outer.Handle("/", AuthMiddleware(a2aCfg.Auth, cfg.JWTService, cfg.TokenProvider)(inner))
	} else {
		outer.Handle("/", inner)
	}

	addr := ":" + strconv.Itoa(a2aCfg.Port)
	log.Printf("[a2a] starting server for agent %s on %s", name, addr)
	if err := http.ListenAndServe(addr, outer); err != nil {
		log.Printf("[a2a] server for agent %s stopped: %v", name, err)
	}
}

func startGenericAgentServer(ctx context.Context, cfg *GenericServerConfig, ag *ExposedAgent) {
	name := strings.TrimSpace(ag.Name)
	if name == "" {
		name = strings.TrimSpace(ag.ID)
	}
	if name == "" || ag.A2A == nil {
		return
	}

	card := AgentCard{
		Name:         name,
		Description:  strings.TrimSpace(ag.Description),
		Capabilities: &AgentCapabilities{Streaming: ag.A2A.Streaming},
	}
	if ag.Auth != nil && ag.Auth.Enabled {
		card.Authentication = map[string]interface{}{
			"type":       "bearer",
			"resource":   ag.Auth.Resource,
			"scopes":     ag.Auth.Scopes,
			"useIDToken": ag.Auth.UseIDToken,
		}
	}

	inner := http.NewServeMux()
	inner.HandleFunc("POST /v1/message:send", handleGenericMessageSend(cfg, ag))
	if ag.A2A.Streaming {
		inner.HandleFunc("POST /v1/message:stream", handleGenericMessageStream(cfg, ag))
	}

	outer := http.NewServeMux()
	outer.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})

	if ag.Auth != nil && ag.Auth.Enabled {
		outer.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
			meta := map[string]interface{}{"resource": ag.Auth.Resource}
			if len(ag.Auth.Scopes) > 0 {
				meta["scopes_supported"] = ag.Auth.Scopes
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(meta)
		})
		outer.Handle("/", AuthMiddleware(&agentmodel.A2AAuth{
			Enabled:       ag.Auth.Enabled,
			Resource:      ag.Auth.Resource,
			Scopes:        append([]string(nil), ag.Auth.Scopes...),
			UseIDToken:    ag.Auth.UseIDToken,
			ExcludePrefix: ag.Auth.ExcludePrefix,
		}, cfg.JWTService, cfg.TokenProvider)(inner))
	} else {
		outer.Handle("/", inner)
	}

	addr := ":" + strconv.Itoa(ag.A2A.Port)
	log.Printf("[a2a] starting server for agent %s on %s", name, addr)
	if err := http.ListenAndServe(addr, outer); err != nil {
		log.Printf("[a2a] server for agent %s stopped: %v", name, err)
	}
}

func handleGenericMessageSend(cfg *GenericServerConfig, ag *ExposedAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SendMessageRequest
		var rpcReq JSONRPCRequest
		body, err := readBody(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		var rpcID interface{}
		if err := json.Unmarshal(body, &rpcReq); err == nil && rpcReq.JSONRPC == "2.0" && rpcReq.Method != "" {
			rpcID = rpcReq.ID
			if rpcReq.Method != "message/send" {
				writeJSONRPCError(w, rpcID, -32601, "method not found: "+rpcReq.Method)
				return
			}
			if rpcReq.Params != nil {
				if err := json.Unmarshal(rpcReq.Params, &req); err != nil {
					writeJSONRPCError(w, rpcID, -32602, "invalid params: "+err.Error())
					return
				}
			}
		} else {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid request: "+err.Error())
				return
			}
		}

		messages := req.EffectiveMessages()
		if len(messages) == 0 {
			msg := "message with at least one part is required"
			if rpcID != nil {
				writeJSONRPCError(w, rpcID, -32602, msg)
			} else {
				writeJSONError(w, http.StatusBadRequest, msg)
			}
			return
		}

		query := extractText(messages)
		if query == "" {
			msg := "no text content in message"
			if rpcID != nil {
				writeJSONRPCError(w, rpcID, -32602, msg)
			} else {
				writeJSONError(w, http.StatusBadRequest, msg)
			}
			return
		}

		reqCtx := r.Context()
		if bearerTok := iauth.Bearer(reqCtx); bearerTok != "" && iauth.IDToken(reqCtx) == "" {
			reqCtx = iauth.WithIDToken(reqCtx, bearerTok)
		}
		if ag.A2A != nil && ag.A2A.UserCredURL != "" && cfg.ApplyUserCred != nil {
			reqCtx, err = cfg.ApplyUserCred(reqCtx, ag.A2A.UserCredURL)
			if err != nil {
				log.Printf("[a2a] failed to apply user cred for agent %s: %v", ag.ID, err)
			}
		}

		// Decouple turn execution from the HTTP request lifecycle so a client
		// disconnect or server write-timeout does not cancel the running turn.
		out, err := cfg.Query(context.WithoutCancel(reqCtx), ag.ID, query, req.ContextID)
		if err != nil {
			errMsg := err.Error()
			task := Task{
				ID:        fmt.Sprintf("t-%s", req.ContextID),
				ContextID: req.ContextID,
				Status:    TaskStatus{State: TaskStateFailed, Error: &errMsg},
			}
			writeResult(w, rpcID, &SendMessageResponse{Task: task})
			return
		}

		contextID := req.ContextID
		if out != nil && out.ConversationID != "" {
			contextID = out.ConversationID
		}
		taskID := "t-generic"
		if out != nil && strings.TrimSpace(out.MessageID) != "" {
			taskID = "t-" + out.MessageID
		}
		content := ""
		if out != nil {
			content = out.Content
		}
		task := Task{
			ID:        taskID,
			ContextID: contextID,
			Status:    TaskStatus{State: TaskStateCompleted},
			Artifacts: []Artifact{{Parts: []Part{{Type: "text", Text: content}}}},
		}
		writeResult(w, rpcID, &SendMessageResponse{Task: task})
	}
}

func handleGenericMessageStream(cfg *GenericServerConfig, ag *ExposedAgent) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		var req SendMessageRequest
		var rpcReq JSONRPCRequest
		body, err := readBody(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		var rpcID interface{}
		if err := json.Unmarshal(body, &rpcReq); err == nil && rpcReq.JSONRPC == "2.0" && rpcReq.Method != "" {
			rpcID = rpcReq.ID
			if rpcReq.Method != "message/stream" {
				writeJSONRPCError(w, rpcID, -32601, "method not found: "+rpcReq.Method)
				return
			}
			if rpcReq.Params != nil {
				if err := json.Unmarshal(rpcReq.Params, &req); err != nil {
					writeJSONRPCError(w, rpcID, -32602, "invalid params: "+err.Error())
					return
				}
			}
		} else {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid request: "+err.Error())
				return
			}
		}

		messages := req.EffectiveMessages()
		if len(messages) == 0 {
			writeJSONError(w, http.StatusBadRequest, "message with at least one part is required")
			return
		}
		query := extractText(messages)
		if query == "" {
			writeJSONError(w, http.StatusBadRequest, "no text content in message")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		sendEvent := func(event interface{}) {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if rpcID != nil {
				resp := JSONRPCResponse{JSONRPC: "2.0", ID: rpcID, Result: data}
				data, _ = json.Marshal(resp)
			}
			fmt.Fprintf(w, "data:%s\n\n", data)
			flusher.Flush()
		}

		task := &Task{ID: "t-" + uuidString(), ContextID: req.ContextID}
		task.Touch(TaskStateRunning)
		sendEvent(NewStatusEvent(task, false))

		reqCtx := r.Context()
		if bearerTok := iauth.Bearer(reqCtx); bearerTok != "" && iauth.IDToken(reqCtx) == "" {
			reqCtx = iauth.WithIDToken(reqCtx, bearerTok)
		}
		if ag.A2A != nil && ag.A2A.UserCredURL != "" && cfg.ApplyUserCred != nil {
			reqCtx, err = cfg.ApplyUserCred(reqCtx, ag.A2A.UserCredURL)
			if err != nil {
				log.Printf("[a2a] failed to apply user cred for agent %s: %v", ag.ID, err)
			}
		}

		// Decouple turn execution from the HTTP request lifecycle so a client
		// disconnect or server write-timeout does not cancel the running turn.
		out, err := cfg.Query(context.WithoutCancel(reqCtx), ag.ID, query, req.ContextID)
		if err != nil {
			errMsg := err.Error()
			task.Touch(TaskStateFailed)
			task.Status.Error = &errMsg
			sendEvent(NewStatusEvent(task, true))
			return
		}
		if out != nil && out.ConversationID != "" {
			task.ContextID = out.ConversationID
		}
		content := ""
		if out != nil {
			content = out.Content
		}
		art := Artifact{
			ID:        uuidString(),
			CreatedAt: time.Now().UTC(),
			Parts:     []Part{{Type: "text", Text: content}},
		}
		task.Artifacts = append(task.Artifacts, art)
		sendEvent(NewArtifactEvent(task, art, false, true))
		task.Touch(TaskStateCompleted)
		sendEvent(NewStatusEvent(task, true))
	}
}

func uuidString() string {
	return strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

// handleMessageSend bridges an A2A message/send to agent.Query().
func handleMessageSend(cfg *ServerConfig, ag *agentmodel.Agent, a2aCfg *agentmodel.ServeA2A) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Try to parse as JSON-RPC first, fall back to plain SendMessageRequest.
		var req SendMessageRequest
		var rpcReq JSONRPCRequest
		body, err := readBody(r)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}

		var rpcID interface{}
		if err := json.Unmarshal(body, &rpcReq); err == nil && rpcReq.JSONRPC == "2.0" && rpcReq.Method != "" {
			// JSON-RPC envelope — extract params.
			rpcID = rpcReq.ID
			if rpcReq.Method != "message/send" {
				writeJSONRPCError(w, rpcID, -32601, "method not found: "+rpcReq.Method)
				return
			}
			if rpcReq.Params != nil {
				if err := json.Unmarshal(rpcReq.Params, &req); err != nil {
					writeJSONRPCError(w, rpcID, -32602, "invalid params: "+err.Error())
					return
				}
			}
		} else {
			// Plain JSON.
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid request: "+err.Error())
				return
			}
		}

		messages := req.EffectiveMessages()
		if len(messages) == 0 {
			msg := "message with at least one part is required"
			if rpcID != nil {
				writeJSONRPCError(w, rpcID, -32602, msg)
			} else {
				writeJSONError(w, http.StatusBadRequest, msg)
			}
			return
		}

		query := extractText(messages)
		if query == "" {
			msg := "no text content in message"
			if rpcID != nil {
				writeJSONRPCError(w, rpcID, -32602, msg)
			} else {
				writeJSONError(w, http.StatusBadRequest, msg)
			}
			return
		}

		// For A2A inbound calls, the Bearer token IS the caller's IdToken.
		// Ensure it is also available as IDToken for downstream MCP calls
		// that may prefer IdToken over AccessToken.
		reqCtx := r.Context()
		if bearerTok := iauth.Bearer(reqCtx); bearerTok != "" && iauth.IDToken(reqCtx) == "" {
			reqCtx = iauth.WithIDToken(reqCtx, bearerTok)
		}

		// Optionally inject user credentials.
		if a2aCfg.UserCredURL != "" && cfg.ApplyUserCred != nil {
			reqCtx, err = cfg.ApplyUserCred(reqCtx, a2aCfg.UserCredURL)
			if err != nil {
				log.Printf("[a2a] failed to apply user cred for agent %s: %v", ag.ID, err)
			}
		}

		// Execute agent query.
		input := &agentsvc.QueryInput{
			AgentID:        ag.ID,
			Query:          query,
			ConversationID: req.ContextID,
		}
		out := &agentsvc.QueryOutput{}
		// Decouple turn execution from the HTTP request lifecycle so a client
		// disconnect or server write-timeout does not cancel the running turn.
		if err := cfg.AgentService.Query(context.WithoutCancel(reqCtx), input, out); err != nil {
			errMsg := err.Error()
			task := Task{
				ID:        fmt.Sprintf("t-%s", req.ContextID),
				ContextID: req.ContextID,
				Status:    TaskStatus{State: TaskStateFailed, Error: &errMsg},
			}
			writeResult(w, rpcID, &SendMessageResponse{Task: task})
			return
		}

		// Build task response.
		contextID := req.ContextID
		if out.ConversationID != "" {
			contextID = out.ConversationID
		}
		task := Task{
			ID:        "t-" + out.MessageID,
			ContextID: contextID,
			Status:    TaskStatus{State: TaskStateCompleted},
			Artifacts: []Artifact{{
				Parts: []Part{{Type: "text", Text: out.Content}},
			}},
		}

		writeResult(w, rpcID, &SendMessageResponse{Task: task})
	}
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, fmt.Errorf("empty request body")
	}
	defer r.Body.Close()
	var buf [64 * 1024]byte
	var data []byte
	for {
		n, err := r.Body.Read(buf[:])
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return data, nil
}

func writeResult(w http.ResponseWriter, rpcID interface{}, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if rpcID != nil {
		// JSON-RPC envelope.
		resultData, _ := json.Marshal(result)
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      rpcID,
			Result:  resultData,
		}
		_ = json.NewEncoder(w).Encode(resp)
		return
	}
	_ = json.NewEncoder(w).Encode(result)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func writeJSONRPCError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	}
	_ = json.NewEncoder(w).Encode(resp)
}
