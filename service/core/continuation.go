package core

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/runtime/memory"
)

// BuildContinuationRequest constructs a continuation request by selecting the latest
// assistant response anchor (resp.id) and including only tool-call messages that
// map to that anchor.
func (s *Service) BuildContinuationRequest(ctx context.Context, req *llm.GenerateRequest, history *prompt.History) *llm.GenerateRequest {
	var conversationID string
	if meta, ok := memory.TurnMetaFromContext(ctx); ok {
		conversationID = meta.ConversationID
	}
	if conversationID == "" {
		conversationID = memory.ConversationIDFromContext(ctx)
	}

	anchor := history.LastResponse
	if req == nil || strings.TrimSpace(conversationID) == "" || anchor == nil || !anchor.IsValid() || len(history.Traces) == 0 {
		if debugtrace.Enabled() {
			debugtrace.Write("core", "continuation_skipped", map[string]any{
				"conversationID": strings.TrimSpace(conversationID),
				"reason":         continuationSkipReason(req, conversationID, history),
			})
		}
		return nil
	}

	// Anchor derived from binding History.LastResponse
	anchorID := strings.TrimSpace(anchor.ID)
	if anchorID == "" {
		if debugtrace.Enabled() {
			debugtrace.Write("core", "continuation_skipped", map[string]any{
				"conversationID": strings.TrimSpace(conversationID),
				"reason":         "empty_anchor_id",
			})
		}
		return nil
	}

	// Collect tool-call messages mapped to this anchor. User messages
	// are already part of the anchored context and do not participate
	// in continuation-by-anchor.
	var selected llm.Messages
	assistantToolCallCount := 0
	toolResultCount := 0
	expectedToolCallIDs := make([]string, 0)
	toolResultIDs := make([]string, 0)
	for _, m := range req.Messages {

		if len(m.ToolCalls) > 0 {
			filtered := filterToolCallsByAnchor(m.ToolCalls, history, anchorID)
			if len(filtered) == 0 {
				continue
			}
			assistantToolCallCount += len(filtered)
			for _, call := range filtered {
				if id := strings.TrimSpace(call.ID); id != "" {
					expectedToolCallIDs = append(expectedToolCallIDs, id)
				}
			}
			copyMsg := m
			copyMsg.ToolCalls = filtered
			selected.Append(copyMsg)
			continue
		}

		if m.ToolCallId != "" {
			key := prompt.KindToolCall.Key(m.ToolCallId)
			trace, ok := history.Traces[key]
			if !ok || trace.ID != anchorID {
				continue
			}
			toolResultCount++
			toolResultIDs = append(toolResultIDs, strings.TrimSpace(m.ToolCallId))
			selected.Append(m)
			continue
		}

		if m.Content != "" {
			if llm.MessageRole(m.Role) != llm.RoleUser {
				continue
			}

			key := prompt.KindContent.Key(m.Content)
			trace, ok := history.Traces[key]

			if !ok || trace.At.Before(anchor.At) || trace.At.Equal(anchor.At) {
				continue
			}

			selected.Append(m)
			continue
		}
	}

	if len(selected) == 0 {
		if debugtrace.Enabled() {
			debugtrace.Write("core", "continuation_skipped", map[string]any{
				"conversationID": strings.TrimSpace(conversationID),
				"reason":         "no_selected_messages",
				"anchorID":       anchorID,
			})
		}
		return nil
	}
	// OpenAI Responses continuation has proven fragile for multi-tool anchors.
	// Fall back to full transcript in those cases rather than sending an
	// incomplete/mismatched function_call_output set under previous_response_id.
	if assistantToolCallCount > 1 || toolResultCount > 1 || (assistantToolCallCount > 0 && toolResultCount < assistantToolCallCount) {
		if debugtrace.Enabled() {
			debugtrace.Write("core", "continuation_skipped", map[string]any{
				"conversationID":      strings.TrimSpace(conversationID),
				"reason":              "multi_tool_anchor_fallback",
				"anchorID":            anchorID,
				"assistantToolCalls":  assistantToolCallCount,
				"toolResultMessages":  toolResultCount,
				"expectedToolCallIDs": expectedToolCallIDs,
				"toolResultIDs":       toolResultIDs,
				"selectedMessages":    debugtrace.SummarizeMessages(selected),
			})
		}
		return nil
	}

	// Build continuation request with selected tool-call messages
	continuationRequest := &llm.GenerateRequest{}
	if req.Options != nil {
		opts := *req.Options
		continuationRequest.Options = &opts
	}
	continuationRequest.Messages = append(continuationRequest.Messages, selected...)
	continuationRequest.PreviousResponseID = anchorID
	if debugtrace.Enabled() {
		debugtrace.Write("core", "continuation_request", map[string]any{
			"conversationID":     strings.TrimSpace(conversationID),
			"anchorID":           anchorID,
			"selectedMessageCnt": len(selected),
			"selectedMessages":   debugtrace.SummarizeMessages(selected),
		})
	}
	return continuationRequest
}

func continuationSkipReason(req *llm.GenerateRequest, conversationID string, history *prompt.History) string {
	switch {
	case req == nil:
		return "nil_request"
	case strings.TrimSpace(conversationID) == "":
		return "missing_conversation_id"
	case history == nil:
		return "nil_history"
	case history.LastResponse == nil:
		return "missing_last_response"
	case !history.LastResponse.IsValid():
		return "invalid_last_response"
	case len(history.Traces) == 0:
		return "empty_traces"
	default:
		return "unknown"
	}
}

func filterToolCallsByAnchor(toolCalls []llm.ToolCall, history *prompt.History, anchorID string) []llm.ToolCall {
	if len(toolCalls) == 0 || history == nil || anchorID == "" {
		return nil
	}
	var filtered []llm.ToolCall
	for _, call := range toolCalls {
		key := prompt.KindToolCall.Key(call.ID)
		trace, ok := history.Traces[key]
		if !ok || trace.ID != anchorID {
			continue
		}
		filtered = append(filtered, call)
	}
	return filtered
}
