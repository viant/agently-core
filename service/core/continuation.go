package core

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// BuildContinuationRequest constructs a continuation request by selecting the latest
// assistant response anchor (resp.id) and including only tool-call messages that
// map to that anchor.
//
// Safety rules:
//   - Continuation is only valid when every tool call produced by the anchored
//     iteration has a corresponding tool result selected for replay. Partial
//     tool-result continuation can cause provider-side errors because the
//     anchor still expects the full set of function/tool outputs for that
//     iteration.
//   - Leading or mid-history system messages do not disable continuation by
//     themselves. The provider-side hard requirement is complete tool-call
//     replay for the anchored response; dropping that continuity causes errors.
func (s *Service) BuildContinuationRequest(ctx context.Context, req *llm.GenerateRequest, history *binding.History) *llm.GenerateRequest {
	var conversationID string
	if meta, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		conversationID = meta.ConversationID
	}
	if conversationID == "" {
		conversationID = runtimerequestctx.ConversationIDFromContext(ctx)
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

	// Debug: log all traces and their anchor mappings
	traceDetails := make([]map[string]string, 0)
	for key, trace := range history.Traces {
		if trace == nil {
			continue
		}
		traceDetails = append(traceDetails, map[string]string{
			"key":     key,
			"traceID": trace.ID,
			"kind":    string(trace.Kind),
			"matchesAnchor": func() string {
				if trace.ID == anchorID {
					return "YES"
				}
				return "no"
			}(),
		})
	}
	debugtrace.LogToFile("core", "continuation_trace_map", map[string]interface{}{
		"anchorID": anchorID,
		"traces":   traceDetails,
		"msgCount": len(req.Messages),
	})

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
			key := binding.KindToolCall.Key(m.ToolCallId)
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
			role := llm.MessageRole(m.Role)
			if role != llm.RoleUser && role != llm.RoleAssistant {
				continue
			}

			key := binding.KindContent.Key(m.Content)
			trace, ok := history.Traces[key]

			if !ok || trace.At.Before(anchor.At) || trace.At.Equal(anchor.At) {
				continue
			}

			selected.Append(m)
			continue
		}
	}

	if len(selected) == 0 {
		debugtrace.LogToFile("core", "continuation_rejected", map[string]interface{}{
			"reason":   "no_selected_messages",
			"anchorID": anchorID,
		})
		return nil
	}
	// Guard: reject continuation when tool results are incomplete.
	// Multi-tool continuation is fine as long as every tool call has a matching
	// result — the provider sends all function_call_output items together.
	if assistantToolCallCount > 0 && toolResultCount < assistantToolCallCount {
		debugtrace.LogToFile("core", "continuation_rejected", map[string]interface{}{
			"reason":              "multi_tool_guard",
			"anchorID":            anchorID,
			"assistantToolCalls":  assistantToolCallCount,
			"toolResults":         toolResultCount,
			"expectedToolCallIDs": expectedToolCallIDs,
			"toolResultIDs":       toolResultIDs,
		})
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

	// Cross-check: count how many tool calls the anchor actually produced
	// (from the traces map) vs how many we selected. If they disagree, a tool
	// result was dropped (e.g. missing payload) and continuing would trigger
	// "No tool output found" from the provider.
	anchorToolCallIDs := make([]string, 0)
	for key, trace := range history.Traces {
		if trace == nil || trace.Kind != binding.KindToolCall || trace.ID != anchorID {
			continue
		}
		// Extract the opID from the key (format "toolcall:<opID>")
		if idx := strings.Index(key, ":"); idx >= 0 && idx+1 < len(key) {
			anchorToolCallIDs = append(anchorToolCallIDs, key[idx+1:])
		}
	}
	debugtrace.LogToFile("core", "continuation_cross_check", map[string]interface{}{
		"anchorID":            anchorID,
		"anchorToolCallCount": len(anchorToolCallIDs),
		"anchorToolCallIDs":   anchorToolCallIDs,
		"selectedToolResults": toolResultCount,
		"selectedToolCalls":   assistantToolCallCount,
	})
	if len(anchorToolCallIDs) > 0 && toolResultCount < len(anchorToolCallIDs) {
		if debugtrace.Enabled() {
			debugtrace.Write("core", "continuation_skipped", map[string]any{
				"conversationID":      strings.TrimSpace(conversationID),
				"reason":              "anchor_tool_count_mismatch",
				"anchorID":            anchorID,
				"anchorToolCallIDs":   anchorToolCallIDs,
				"selectedToolResults": toolResultCount,
				"selectedToolCalls":   assistantToolCallCount,
				"expectedToolCallIDs": expectedToolCallIDs,
				"toolResultIDs":       toolResultIDs,
			})
		}
		return nil
	}

	// Build continuation request with selected tool-call messages
	continuationRequest := &llm.GenerateRequest{}
	if req.Options != nil {
		opts := *req.Options
		if strings.TrimSpace(opts.Mode) == "" {
			opts.Mode = strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx))
		}
		continuationRequest.Options = &opts
	} else if mode := strings.TrimSpace(runtimerequestctx.RequestModeFromContext(ctx)); mode != "" {
		continuationRequest.Options = &llm.Options{Mode: mode}
	}
	continuationRequest.Messages = append(continuationRequest.Messages, selected...)
	continuationRequest.PreviousResponseID = anchorID
	if debugtrace.Enabled() {
		debugtrace.Write("core", "continuation_request", map[string]any{
			"conversationID":     strings.TrimSpace(conversationID),
			"anchorID":           anchorID,
			"selectedMessageCnt": len(selected),
			"anchorToolCallCnt":  len(anchorToolCallIDs),
			"selectedMessages":   debugtrace.SummarizeMessages(selected),
		})
	}
	return continuationRequest
}

func continuationSkipReason(req *llm.GenerateRequest, conversationID string, history *binding.History) string {
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

func filterToolCallsByAnchor(toolCalls []llm.ToolCall, history *binding.History, anchorID string) []llm.ToolCall {
	if len(toolCalls) == 0 || history == nil || anchorID == "" {
		return nil
	}
	var filtered []llm.ToolCall
	for _, call := range toolCalls {
		key := binding.KindToolCall.Key(call.ID)
		trace, ok := history.Traces[key]
		if !ok || trace.ID != anchorID {
			continue
		}
		filtered = append(filtered, call)
	}
	return filtered
}
