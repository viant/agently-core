package sdk

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"
)

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
		logx.Debugf("sse", "client connected convo=%q", convID)
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
		for {
			select {
			case ev, open := <-sub.C():
				if !open {
					logx.Debugf("sse", "channel closed convo=%q", convID)
					return
				}
				startedAt := ""
				if ev.StartedAt != nil && !ev.StartedAt.IsZero() {
					startedAt = ev.StartedAt.Format(time.RFC3339Nano)
				}
				completedAt := ""
				if ev.CompletedAt != nil && !ev.CompletedAt.IsZero() {
					completedAt = ev.CompletedAt.Format(time.RFC3339Nano)
				}
				logx.Debugf("sse", "sending type=%q op=%q convo=%q stream_id=%q turn=%q mode=%q agent=%q agent_name=%q user_msg=%q assistant_msg=%q parent_msg=%q model_call=%q tool=%q toolCallId=%q toolMsgId=%q status=%q final=%v iter=%d page=%d/%d latest=%v linked=%q feed=%q created_at=%q started_at=%q completed_at=%q sent_at=%q req=%q resp=%q preq=%q presp=%q stream=%q",
					string(ev.Type), ev.Op, ev.ConversationID, ev.StreamID, ev.TurnID, ev.Mode, ev.AgentIDUsed, ev.AgentName, ev.UserMessageID, ev.AssistantMessageID, ev.ParentMessageID, ev.ModelCallID, ev.ToolName, ev.ToolCallID, ev.ToolMessageID, ev.Status, ev.FinalResponse, ev.Iteration, ev.PageIndex, ev.PageCount, ev.LatestPage, ev.LinkedConversationID, ev.FeedID,
					ev.CreatedAt.Format(time.RFC3339Nano), startedAt, completedAt, time.Now().Format(time.RFC3339Nano), ev.RequestPayloadID, ev.ResponsePayloadID, ev.ProviderRequestPayloadID, ev.ProviderResponsePayloadID, ev.StreamPayloadID)
				data, _ := json.Marshal(ev)
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
				logx.Debugf("sse", "client disconnected convo=%q", convID)
				return
			}
		}
	}
}
