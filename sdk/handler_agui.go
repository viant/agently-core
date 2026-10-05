package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	svcauth "github.com/viant/agently-core/service/auth"
)

// The initial boundary uses the existing server-owned conversation history.
// Only the final user message is submitted; earlier messages are a client view,
// not an instruction to rewrite persisted history. Disconnect detaches observation
// just as the existing query transport does; it does not cancel backend work.
func handleAGUIRun(client Client, authCfg *svcauth.Config, bindings ...AGUIWorkspaceBindings) http.HandlerFunc {
	if runtime, ok := client.(aguiRuntime); ok {
		return handleAGUIDurable(client, runtime, authCfg, bindings...)
	}
	var mu sync.Mutex
	active := map[string]bool{}
	return func(w http.ResponseWriter, r *http.Request) {
		var input agui.RunAgentInput
		if err := decodeAGUIRequest(w, r, &input); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		ext, err := validateAGUIInput(&input)
		if err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		userID := resolveQueryUserID(w, r, "", authCfg)
		if userID == "" {
			httpError(w, http.StatusUnauthorized, fmt.Errorf("authorization required"))
			return
		}
		ctx := r.Context()
		if iauth.EffectiveUserID(ctx) == "" {
			ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: userID})
		}
		if _, ok := w.(http.Flusher); !ok {
			httpError(w, http.StatusInternalServerError, fmt.Errorf("streaming response unavailable"))
			return
		}
		translator := agui.NewTranslator(input.ThreadID, input.RunID)
		if ext != nil && ext.Operation == "capabilities" {
			beginAGUIStream(w)
			if !writeAGUIEvents(w, translator.Start()) {
				return
			}
			if !writeAGUIEvents(w, []agui.Event{aguiCapabilities()}) {
				return
			}
			writeAGUIEvents(w, translator.Finish("success"))
			return
		}
		// Authenticate/authorize before subscribing: the internal bus is not an
		// authorization boundary. A caller must own an existing thread to run it.
		conversation, err := client.GetConversation(ctx, input.ThreadID)
		if err != nil {
			httpError(w, http.StatusForbidden, fmt.Errorf("thread unavailable"))
			return
		}
		if conversation != nil && (conversation.CreatedByUserId == nil || *conversation.CreatedByUserId != userID) {
			httpError(w, http.StatusForbidden, fmt.Errorf("thread is not owned by the current user"))
			return
		}
		last := input.Messages[len(input.Messages)-1]
		// Reject retries instead of executing the same persisted user message twice.
		if conversation != nil {
			page, err := client.GetMessages(ctx, &GetMessagesInput{ConversationID: input.ThreadID, TurnID: last.ID})
			if err != nil {
				httpError(w, http.StatusInternalServerError, fmt.Errorf("could not check message identity"))
				return
			}
			if page != nil && len(page.Rows) > 0 {
				httpError(w, http.StatusConflict, fmt.Errorf("message already submitted; replay is not supported"))
				return
			}
		}
		key := input.ThreadID + "\x00" + last.ID
		runKey := input.ThreadID + "\x00run\x00" + input.RunID
		mu.Lock()
		if active[key] || active[runKey] {
			mu.Unlock()
			httpError(w, http.StatusConflict, fmt.Errorf("message or run is already active"))
			return
		}
		active[key], active[runKey] = true, true
		mu.Unlock()
		executionDone := make(chan struct{})
		executionStarted := false
		release := func() { mu.Lock(); delete(active, key); delete(active, runKey); mu.Unlock() }
		defer func() {
			if !executionStarted {
				release()
				return
			}
			select {
			case <-executionDone:
				release()
			default:
				// Detaching observation must not admit a duplicate while Query
				// is still accepting/persisting this message in the background.
				go func() { <-executionDone; release() }()
			}
		}()
		matches := func(ev *streaming.Event) bool {
			return ev != nil && (ev.ConversationID == input.ThreadID || (ev.ConversationID == "" && ev.StreamID == input.ThreadID)) && ev.TurnID == last.ID
		}
		sub, err := client.StreamEvents(ctx, &StreamEventsInput{ConversationID: input.ThreadID, Filter: matches})
		if err != nil {
			httpError(w, http.StatusServiceUnavailable, fmt.Errorf("stream subscription unavailable: %w", err))
			return
		}
		defer sub.Close()
		var text string
		_ = json.Unmarshal(last.Content, &text) // validated before accepting the run
		query := &agentsvc.QueryInput{ConversationID: input.ThreadID, MessageID: last.ID, UserId: userID, Query: text, DisplayQuery: text}
		if err := applyAGUIExecution(query, ext, false); err != nil {
			httpError(w, http.StatusBadRequest, err)
			return
		}
		beginAGUIStream(w)
		if !writeAGUIEvents(w, translator.Start()) {
			return
		}
		type queryResult struct {
			output *agentsvc.QueryOutput
			err    error
		}
		results := make(chan queryResult, 1)
		executionStarted = true
		go func() {
			defer close(executionDone)
			out, err := client.Query(context.WithoutCancel(ctx), query)
			results <- queryResult{out, err}
		}()
		queued := false
		sawText := false
		consume := func(ev *streaming.Event) bool {
			if !matches(ev) {
				return true
			}
			if ev.Type == streaming.EventTypeTurnQueued {
				queued = true
			}
			copy := *ev
			if copy.AssistantMessageID == last.ID {
				copy.AssistantMessageID = ""
			}
			if copy.Type == streaming.EventTypeTextDelta || (copy.Type == streaming.EventTypeAssistant && copy.Patch["role"] != "user" && copy.Content != "") {
				sawText = true
			}
			if err := hydrateAGUITool(ctx, client, &copy); err != nil {
				return writeAGUIEvents(w, translator.Fail(err.Error(), "PAYLOAD_UNAVAILABLE"))
			}
			return writeAGUIEvents(w, translator.Translate(&copy))
		}
		ticker := time.NewTicker(streamKeepaliveInterval)
		defer ticker.Stop()
		for !translator.Done() {
			select {
			case <-r.Context().Done():
				return
			case ev, open := <-sub.C():
				if !open {
					writeAGUIEvents(w, translator.Fail("stream closed before run completion: "+sub.Reason(), "STREAM_CLOSED"))
					return
				}
				if !consume(ev) {
					return
				}
			case result := <-results:
				results = nil
				// Query publishes lifecycle events synchronously before returning.
				// Drain those first so queued returns and terminal events cannot race
				// a preset-answer fallback or duplicate the completed text.
				draining := true
				for draining && !translator.Done() {
					select {
					case ev, open := <-sub.C():
						if !open {
							writeAGUIEvents(w, translator.Fail("stream closed before run completion: "+sub.Reason(), "STREAM_CLOSED"))
							return
						}
						if !consume(ev) {
							return
						}
					default:
						draining = false
					}
				}
				if translator.Done() {
					return
				}
				if result.err != nil {
					writeAGUIEvents(w, translator.Fail(result.err.Error(), "EXECUTION_FAILED"))
					return
				}
				if queued {
					continue
				}
				if result.output != nil && result.output.Elicitation != nil {
					writeAGUIEvents(w, translator.Fail("interrupt continuation is not supported by this profile yet", "UNSUPPORTED_INTERRUPT"))
					return
				}
				if result.output != nil && result.output.Content != "" && !sawText {
					// Preset answers normally publish their own assistant event.
					// Use the synchronous result only when no text was streamed.
					if !writeAGUIEvents(w, translator.CompleteText(input.RunID+":answer", result.output.Content)) {
						return
					}
				}
				writeAGUIEvents(w, translator.Finish("success"))
				return
			case <-ticker.C:
				if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}
}

func decodeAGUIRequest(w http.ResponseWriter, r *http.Request, input *agui.RunAgentInput) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if err = agui.ValidateInput(data); err != nil {
		return err
	}
	return json.Unmarshal(data, input)
}

func validateAGUIInput(input *agui.RunAgentInput) (*agui.Extension, error) {
	if strings.TrimSpace(input.ThreadID) == "" || strings.TrimSpace(input.RunID) == "" || input.Messages == nil {
		return nil, fmt.Errorf("threadId, runId and messages are required")
	}
	if input.ProtocolVersion != "" && input.ProtocolVersion != agui.ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocolVersion")
	}
	if input.ParentRunID != "" || len(input.Resume) > 0 || len(input.Tools) > 0 || len(input.Context) > 0 {
		return nil, fmt.Errorf("parent runs, resume, client tools and supplied context are not supported by this profile yet")
	}
	if len(input.State) > 0 && string(bytes.TrimSpace(input.State)) != "{}" && string(bytes.TrimSpace(input.State)) != "null" {
		var state map[string]any
		if json.Unmarshal(input.State, &state) != nil || len(state) != 0 {
			return nil, fmt.Errorf("shared state input is not supported by this profile yet")
		}
	}
	var forwarded agui.ForwardedProps
	if len(input.ForwardedProps) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(input.ForwardedProps))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&forwarded); err != nil {
			return nil, fmt.Errorf("invalid Agently extension: %w", err)
		}
	}
	ext := forwarded.Agently
	if ext != nil {
		if ext.Version != agui.ExtensionVersion || (ext.Operation != "chat" && ext.Operation != "capabilities") {
			return nil, fmt.Errorf("unsupported Agently extension version or operation")
		}
		if ext.Operation == "capabilities" {
			return ext, nil
		}
	}
	if len(input.Messages) == 0 {
		return nil, fmt.Errorf("chat requires a new user message")
	}
	seen := map[string]bool{}
	for _, m := range input.Messages {
		if strings.TrimSpace(m.ID) == "" || seen[m.ID] {
			return nil, fmt.Errorf("message IDs must be nonempty and unique")
		}
		seen[m.ID] = true
	}
	last := input.Messages[len(input.Messages)-1]
	var content string
	if last.Role != "user" || json.Unmarshal(last.Content, &content) != nil || strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("chat requires a final user message with nonempty text content")
	}
	return ext, nil
}

func aguiCapabilities() agui.Event {
	return agui.Event{Type: "CUSTOM", Name: "agently.capabilities", Value: agui.CapabilitiesValue{
		Version: agui.ExtensionVersion,
		Capabilities: agui.AgentCapabilities{"custom": map[string]any{"agently": map[string]any{
			"version": agui.ExtensionVersion, "operations": []string{"chat", "capabilities"},
			"execution": aguiExecutionCapabilities(),
			"history":   "server", "disconnect": "detach", "replay": false,
		}}},
	}}
}

func beginAGUIStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeAGUIEvents(w http.ResponseWriter, events []agui.Event) bool {
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return false
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
	}
	if len(events) > 0 {
		w.(http.Flusher).Flush()
	}
	return true
}

// Persistence events carry payload references rather than the tool result body.
// Resolve those behind the protocol boundary, through the authenticated backend.
func hydrateAGUITool(ctx context.Context, client Client, event *streaming.Event) error {
	switch event.Type {
	case streaming.EventTypeToolCallStarted, streaming.EventTypeToolCallCompleted, streaming.EventTypeToolCallFailed, streaming.EventTypeToolCallCanceled:
	default:
		return nil
	}
	if event.ToolMessageID == "" {
		return nil
	}
	if event.Arguments == nil && event.RequestPayloadID == "" && event.Type != streaming.EventTypeToolCallStarted {
		// A completion patch contains only changed columns. Its request payload
		// may already be persisted without being repeated in the event.
		if reader, ok := client.(interface {
			aguiToolRequestPayload(context.Context, string, string, string) (string, error)
		}); ok {
			id, err := reader.aguiToolRequestPayload(ctx, event.ConversationID, event.ToolMessageID, event.ToolCallID)
			if err != nil {
				return err
			}
			event.RequestPayloadID = id
		}
	}
	var ids []string
	if event.Arguments == nil && event.RequestPayloadID != "" {
		ids = append(ids, event.RequestPayloadID)
	}
	if event.ResponsePayload == nil && event.Content == "" && event.ResponsePayloadID != "" {
		ids = append(ids, event.ResponsePayloadID)
	}
	if len(ids) == 0 {
		return nil
	}
	payloads, err := client.GetPayloads(ctx, ids)
	if err != nil {
		return fmt.Errorf("tool payload lookup failed: %w", err)
	}
	for _, id := range ids {
		payload := payloads[id]
		if payload == nil || payload.InlineBody == nil {
			return fmt.Errorf("tool payload %q unavailable", id)
		}
		body, compression := payloadResponseBody(payload)
		if compression != "" && !strings.EqualFold(compression, "none") {
			return fmt.Errorf("unsupported tool payload compression %q", compression)
		}
		if id == event.RequestPayloadID {
			if err := json.Unmarshal(body, &event.Arguments); err != nil {
				return fmt.Errorf("tool arguments are not a JSON object")
			}
		} else {
			event.Content = string(body)
		}
	}
	return nil
}

// aguiToolRequestPayload is an internal boundary lookup, not another public API.
func (c *backendClient) aguiToolRequestPayload(ctx context.Context, threadID, messageID, toolCallID string) (string, error) {
	if c.data == nil {
		return "", fmt.Errorf("tool argument lookup unavailable")
	}
	row, err := c.data.GetMessage(ctx, messageID, nil)
	if err != nil {
		return "", fmt.Errorf("tool argument lookup failed: %w", err)
	}
	if row == nil || row.ConversationId != threadID || row.MessageToolCall == nil || row.MessageToolCall.OpId != toolCallID {
		return "", fmt.Errorf("tool argument record unavailable")
	}
	if row.MessageToolCall.RequestPayloadId == nil {
		return "", nil
	}
	return *row.MessageToolCall.RequestPayloadId, nil
}
