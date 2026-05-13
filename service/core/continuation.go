package core

import (
	"context"
	"sort"
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
// Continuation replays the exact tool-call/tool-result slice for the latest
// anchored provider response. If replay state is wrong, the producer needs to
// emit the missing tool-call trace or tool result rather than teaching the
// continuation layer to guess around the gap.
func (s *Service) BuildContinuationRequest(ctx context.Context, req *llm.GenerateRequest, history *binding.History) *llm.GenerateRequest {
	if ctx == nil {
		ctx = context.Background()
	}
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
	selectedToolCallIDs := map[string]struct{}{}
	seenSelectedToolCalls := map[string]struct{}{}
	seenSelectedToolResults := map[string]struct{}{}

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
			unique := make([]llm.ToolCall, 0, len(filtered))
			for _, call := range filtered {
				id := strings.TrimSpace(call.ID)
				if id == "" {
					continue
				}
				if _, ok := seenSelectedToolCalls[id]; ok {
					continue
				}
				seenSelectedToolCalls[id] = struct{}{}
				selectedToolCallIDs[id] = struct{}{}
				unique = append(unique, call)
			}
			if len(unique) == 0 {
				continue
			}
			assistantToolCallCount += len(unique)
			copyMsg := m
			copyMsg.ToolCalls = unique
			selected.Append(copyMsg)
			continue
		}

		if m.ToolCallId != "" {
			toolCallID := strings.TrimSpace(m.ToolCallId)
			if toolCallID == "" {
				continue
			}
			if _, ok := seenSelectedToolResults[toolCallID]; ok {
				continue
			}
			key := binding.KindToolCall.Key(m.ToolCallId)
			trace, ok := history.Traces[key]
			if !ok || trace.ID != anchorID {
				continue
			}
			seenSelectedToolResults[toolCallID] = struct{}{}
			toolResultCount++
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
	anchorToolCallIDs := make([]string, 0)
	anchorToolCallSet := map[string]struct{}{}
	for key, trace := range history.Traces {
		if trace == nil || trace.Kind != binding.KindToolCall || trace.ID != anchorID {
			continue
		}
		// Extract the opID from the key (format "toolcall:<opID>")
		if idx := strings.Index(key, ":"); idx >= 0 && idx+1 < len(key) {
			opID := key[idx+1:]
			anchorToolCallIDs = append(anchorToolCallIDs, opID)
			anchorToolCallSet[opID] = struct{}{}
		}
	}
	sort.Strings(anchorToolCallIDs)
	missingToolCalls := missingContinuationIDs(anchorToolCallSet, selectedToolCallIDs)
	missingToolResults := missingContinuationIDs(anchorToolCallSet, seenSelectedToolResults)
	debugtrace.LogToFile("core", "continuation_cross_check", map[string]interface{}{
		"anchorID":            anchorID,
		"anchorToolCallCount": len(anchorToolCallIDs),
		"anchorToolCallIDs":   anchorToolCallIDs,
		"selectedToolResults": toolResultCount,
		"selectedToolCalls":   assistantToolCallCount,
		"missingToolCalls":    missingToolCalls,
		"missingToolResults":  missingToolResults,
	})
	if len(anchorToolCallSet) > 0 && (len(missingToolCalls) > 0 || len(missingToolResults) > 0) {
		debugtrace.LogToFile("core", "continuation_rejected", map[string]interface{}{
			"reason":             "incomplete_anchor_tool_replay",
			"anchorID":           anchorID,
			"anchorToolCallIDs":  anchorToolCallIDs,
			"missingToolCalls":   missingToolCalls,
			"missingToolResults": missingToolResults,
		})
		return nil
	}

	// Build continuation request with selected tool-call messages
	continuationRequest := &llm.GenerateRequest{}
	continuationRequest.Instructions = strings.TrimSpace(req.Instructions)
	continuationRequest.PromptCacheKey = strings.TrimSpace(req.PromptCacheKey)
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

func missingContinuationIDs(expected map[string]struct{}, actual map[string]struct{}) []string {
	if len(expected) == 0 {
		return nil
	}
	missing := make([]string, 0)
	for id := range expected {
		if _, ok := actual[id]; ok {
			continue
		}
		missing = append(missing, id)
	}
	sort.Strings(missing)
	return missing
}
