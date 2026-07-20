package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/runtime/streaming"
	authctx "github.com/viant/agently-core/service/auth"
)

type inlineReportWorkspaceCatalog interface {
	InlineReportWorkspaceDataSources(ctx context.Context) ([]string, error)
}

var streamKeepaliveInterval = 30 * time.Second

func handleGetMessages(client Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		input := &GetMessagesInput{
			ConversationID: strings.TrimSpace(q.Get("conversationId")),
			ID:             strings.TrimSpace(q.Get("id")),
			TurnID:         strings.TrimSpace(q.Get("turnId")),
		}
		if input.ConversationID == "" {
			httpError(w, http.StatusBadRequest, fmt.Errorf("conversation ID is required"))
			return
		}
		if roles := strings.TrimSpace(q.Get("roles")); roles != "" {
			input.Roles = strings.Split(roles, ",")
		}
		if types := strings.TrimSpace(q.Get("types")); types != "" {
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
		logx.DebugCtxf(r.Context(), "sse", "client connected convo=%q", convID)
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
		ticker := time.NewTicker(streamKeepaliveInterval)
		defer ticker.Stop()
		assistantContent := map[string]*streamingRenderedState{}
		for {
			select {
			case ev, open := <-sub.C():
				if !open {
					// The bus closed the subscription. If the reason is
					// overflow we owe the client an explicit terminal
					// event carrying the last delivered EventSeq so it
					// can reconnect (and, once Phase 2 resume support
					// lands, pick up from where it was cut off) instead
					// of assuming a clean end-of-stream.
					if reason := sub.Reason(); reason == streaming.ReasonOverflow {
						overflowEv := &streaming.Event{
							Type:           streaming.EventTypeStreamOverflow,
							ConversationID: convID,
							StreamID:       convID,
							EventSeq:       sub.LastSeq(),
							CreatedAt:      time.Now(),
							Status:         reason,
						}
						if data, mErr := json.Marshal(overflowEv); mErr == nil {
							fmt.Fprintf(w, "data:%s\n\n", data)
							if ok {
								flusher.Flush()
							}
						}
						logx.DebugCtxf(ctx, "sse", "overflow convo=%q last_seq=%d", convID, sub.LastSeq())
					} else {
						logx.DebugCtxf(ctx, "sse", "channel closed convo=%q reason=%q", convID, sub.Reason())
					}
					return
				}
				outEvent := enrichStreamingRenderedContentWithCatalog(ctx, ev, assistantContent, client)
				startedAt := ""
				if ev.StartedAt != nil && !ev.StartedAt.IsZero() {
					startedAt = ev.StartedAt.Format(time.RFC3339Nano)
				}
				completedAt := ""
				if ev.CompletedAt != nil && !ev.CompletedAt.IsZero() {
					completedAt = ev.CompletedAt.Format(time.RFC3339Nano)
				}
				logx.DebugCtxf(ctx, "sse", "sending type=%q op=%q convo=%q stream_id=%q turn=%q mode=%q agent=%q agent_name=%q user_msg=%q assistant_msg=%q parent_msg=%q model_call=%q tool=%q toolCallId=%q toolMsgId=%q status=%q final=%v iter=%d page=%d/%d latest=%v linked=%q feed=%q created_at=%q started_at=%q completed_at=%q sent_at=%q req=%q resp=%q preq=%q presp=%q stream=%q",
					string(ev.Type), ev.Op, ev.ConversationID, ev.StreamID, ev.TurnID, ev.Mode, ev.AgentIDUsed, ev.AgentName, ev.UserMessageID, ev.AssistantMessageID, ev.ParentMessageID, ev.ModelCallID, ev.ToolName, ev.ToolCallID, ev.ToolMessageID, ev.Status, ev.FinalResponse, ev.Iteration, ev.PageIndex, ev.PageCount, ev.LatestPage, ev.LinkedConversationID, ev.FeedID,
					ev.CreatedAt.Format(time.RFC3339Nano), startedAt, completedAt, time.Now().Format(time.RFC3339Nano), ev.RequestPayloadID, ev.ResponsePayloadID, ev.ProviderRequestPayloadID, ev.ProviderResponsePayloadID, ev.StreamPayloadID)
				data, _ := json.Marshal(outEvent)
				fmt.Fprintf(w, "data:%s\n\n", data)
				if ok {
					flusher.Flush()
				}
			case <-ticker.C:
				fmt.Fprintf(w, ": keepalive\n\n")
				if ok {
					flusher.Flush()
				}
			case <-ctx.Done():
				logx.DebugCtxf(ctx, "sse", "client disconnected convo=%q", convID)
				return
			}
		}
	}
}

// enrichStreamingRenderedContent keeps progressive report assembly on the
// server. Clients receive the same canonical snapshot and only project it into
// their platform-specific live execution state.
type streamingRenderedState struct {
	content     string
	fingerprint string
}

func enrichStreamingRenderedContent(ev *streaming.Event, assistantContent map[string]*streamingRenderedState) *streaming.Event {
	return enrichStreamingRenderedContentWithCatalog(context.Background(), ev, assistantContent, nil)
}

func enrichStreamingRenderedContentWithCatalog(
	ctx context.Context,
	ev *streaming.Event,
	assistantContent map[string]*streamingRenderedState,
	catalogProvider interface{},
) *streaming.Event {
	if ev == nil {
		return nil
	}
	outEvent := *ev
	messageID := strings.TrimSpace(ev.AssistantMessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(ev.MessageID)
	}
	if messageID == "" {
		return &outEvent
	}
	state := assistantContent[messageID]
	if state == nil {
		state = &streamingRenderedState{}
		assistantContent[messageID] = state
	}
	terminal := ev.Type == streaming.EventTypeModelCompleted ||
		ev.Type == streaming.EventTypeAssistant ||
		ev.Type == streaming.EventTypeTurnCompleted ||
		ev.Type == streaming.EventTypeTurnFailed ||
		ev.Type == streaming.EventTypeTurnCanceled
	boundary := false
	switch ev.Type {
	case streaming.EventTypeTextDelta:
		boundary = containsStreamingFenceBoundary(state.content, ev.Content)
		state.content += ev.Content
	case streaming.EventTypeModelCompleted, streaming.EventTypeAssistant,
		streaming.EventTypeTurnCompleted, streaming.EventTypeTurnFailed, streaming.EventTypeTurnCanceled:
		if ev.Content != "" {
			state.content = ev.Content
		}
	}
	if !boundary && !terminal {
		return &outEvent
	}
	if rendered := normalizeRenderedContent(state.content, terminal); rendered != nil {
		applyInlineReportWorkspaceCatalog(ctx, rendered, catalogProvider)
		encoded, _ := json.Marshal(rendered)
		fingerprint := string(encoded)
		if terminal || fingerprint != state.fingerprint {
			outEvent.RenderedContent = rendered
			state.fingerprint = fingerprint
		}
	}
	return &outEvent
}

func applyInlineReportWorkspaceCatalog(ctx context.Context, rendered *RenderedContent, provider interface{}) {
	if rendered == nil || len(rendered.Reports) == 0 {
		return
	}
	requiresCatalog := false
	for _, report := range rendered.Reports {
		if len(ValidateInlineReportWorkspaceReferences(report, nil)) > 0 {
			requiresCatalog = true
			break
		}
	}
	if !requiresCatalog {
		return
	}
	var allowed []string
	// Live workspace data is never available to an anonymous transcript. The
	// effective identity comes from the authenticated request context (JWT
	// subject first), not from report-authored metadata.
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) != "" {
		catalog, ok := provider.(inlineReportWorkspaceCatalog)
		if !ok || catalog == nil {
			catalog = nil
		}
		if catalog != nil {
			if values, err := catalog.InlineReportWorkspaceDataSources(ctx); err == nil {
				allowed = values
			}
		}
	}
	authorized := make([]*RenderedReportAssembly, 0, len(rendered.Reports))
	for _, report := range rendered.Reports {
		warnings := ValidateInlineReportWorkspaceReferences(report, allowed)
		if len(warnings) == 0 {
			authorized = append(authorized, report)
			continue
		}
		rendered.Diagnostics = append(rendered.Diagnostics, warnings...)
	}
	rendered.Reports = authorized
}

// applyInlineReportWorkspaceCatalogToState enforces the same effective-user
// datasource policy for hydrated transcripts as the streaming path. Raw
// assistant content remains intact for audit and compatibility, but denied
// report assemblies are never exposed as renderable canonical reports.
func applyInlineReportWorkspaceCatalogToState(ctx context.Context, state *ConversationState, provider interface{}) {
	if state == nil {
		return
	}
	for _, turn := range state.Turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Messages {
			if message != nil {
				applyInlineReportWorkspaceCatalog(ctx, message.RenderedContent, provider)
			}
		}
		if turn.Assistant != nil {
			messages := append([]*AssistantMessageState{turn.Assistant.Narration, turn.Assistant.Final}, turn.Assistant.Messages...)
			for _, message := range messages {
				if message != nil {
					applyInlineReportWorkspaceCatalog(ctx, message.RenderedContent, provider)
				}
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page != nil {
					applyInlineReportWorkspaceCatalog(ctx, page.RenderedContent, provider)
				}
			}
		}
	}
}

func containsStreamingFenceBoundary(previous, delta string) bool {
	tail := previous
	if len(tail) > 2 {
		tail = tail[len(tail)-2:]
	}
	return strings.Contains(tail+delta, "```")
}
