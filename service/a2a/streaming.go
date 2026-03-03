package a2a

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	iauth "github.com/viant/agently-core/internal/auth"
	agentmodel "github.com/viant/agently-core/protocol/agent"
	agentsvc "github.com/viant/agently-core/service/agent"
)

// handleMessageStream handles SSE streaming for A2A message/stream.
// It writes SSE events as the agent processes the query:
//   - status-update (running)
//   - artifact-update (with content)
//   - status-update (completed/failed, final=true)
func handleMessageStream(cfg *ServerConfig, ag *agentmodel.Agent, a2aCfg *agentmodel.ServeA2A) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		// Parse request — same as message/send.
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

		// Set SSE headers.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Helper to send an SSE event.
		sendEvent := func(event interface{}) {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			if rpcID != nil {
				// Wrap in JSON-RPC response envelope.
				resp := JSONRPCResponse{
					JSONRPC: "2.0",
					ID:      rpcID,
					Result:  data,
				}
				data, _ = json.Marshal(resp)
			}
			fmt.Fprintf(w, "data:%s\n\n", data)
			flusher.Flush()
		}

		// Build task envelope.
		task := &Task{
			ID:        "t-" + uuid.New().String(),
			ContextID: req.ContextID,
		}

		// Send running status.
		task.Touch(TaskStateRunning)
		sendEvent(NewStatusEvent(task, false))

		// For A2A inbound calls, ensure bearer is also available as IDToken.
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
		if err := cfg.AgentService.Query(reqCtx, input, out); err != nil {
			errMsg := err.Error()
			task.Touch(TaskStateFailed)
			task.Status.Error = &errMsg
			sendEvent(NewStatusEvent(task, true))
			return
		}

		// Update contextID from response.
		if out.ConversationID != "" {
			task.ContextID = out.ConversationID
		}

		// Send artifact event.
		art := Artifact{
			ID:        uuid.New().String(),
			CreatedAt: time.Now().UTC(),
			Parts:     []Part{{Type: "text", Text: out.Content}},
		}
		task.Artifacts = append(task.Artifacts, art)
		sendEvent(NewArtifactEvent(task, art, false, true))

		// Send final completed status.
		task.Touch(TaskStateCompleted)
		sendEvent(NewStatusEvent(task, true))
	}
}
