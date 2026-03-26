package sdk

import (
	"encoding/json"
	"strings"
	"time"

	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

// BuildCanonicalState converts a transcript into the canonical ConversationState.
// This is the single entry point for producing renderable state from transcript data.
func BuildCanonicalState(conversationID string, turns convstore.Transcript) *ConversationState {
	state := &ConversationState{
		ConversationID: conversationID,
		Turns:          make([]*TurnState, 0, len(turns)),
	}
	for _, turn := range turns {
		if turn == nil {
			continue
		}
		ts := buildTurnState(turn)
		if ts != nil {
			state.Turns = append(state.Turns, ts)
		}
	}
	return state
}

func buildTurnState(turn *convstore.Turn) *TurnState {
	if turn == nil {
		return nil
	}
	ts := &TurnState{
		TurnID:    strings.TrimSpace(turn.Id),
		Status:    canonicalTurnStatus(turn),
		CreatedAt: turn.CreatedAt,
	}
	linkedSeen := map[string]struct{}{}

	// Extract user message, assistant messages, elicitation, linked conversations
	for _, msg := range turn.Message {
		if msg == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "user":
			if ts.User == nil && msg.Interim == 0 {
				// Prefer RawContent (original user query) over Content
				// (which may be the expanded/internal prompt).
				content := stringValue(msg.RawContent)
				if content == "" {
					content = stringValue(msg.Content)
				}
				ts.User = &UserMessageState{
					MessageID: msg.Id,
					Content:   content,
				}
			}
		}
		// Collect elicitation state
		if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
			ts.Elicitation = buildElicitationState(msg)
		}
		// Collect linked conversations from messages
		if msg.LinkedConversationId != nil && strings.TrimSpace(*msg.LinkedConversationId) != "" {
			appendLinkedConversationState(ts, linkedSeen, &LinkedConversationState{
				ConversationID: strings.TrimSpace(*msg.LinkedConversationId),
				CreatedAt:      msg.CreatedAt,
				ParentTurnID:   stringValue(msg.TurnId),
				ToolCallID:     firstToolCallID(msg.ToolMessage),
			})
		}
		for _, tm := range msg.ToolMessage {
			if tm == nil || tm.LinkedConversationId == nil || strings.TrimSpace(*tm.LinkedConversationId) == "" {
				continue
			}
			appendLinkedConversationState(ts, linkedSeen, &LinkedConversationState{
				ConversationID: strings.TrimSpace(*tm.LinkedConversationId),
				CreatedAt:      tm.CreatedAt,
				ParentTurnID:   stringValue(msg.TurnId),
				ToolCallID:     toolCallIDFromToolMessage(tm),
			})
		}
	}

	// Build execution state from messages with model calls
	pages := buildExecutionPages(turn)
	if len(pages) > 0 {
		ts.Execution = &ExecutionState{
			Pages:         pages,
			ActivePageIdx: len(pages) - 1,
		}
		// Calculate total elapsed from first page start to last page end
		ts.Execution.TotalElapsedMs = calcTotalElapsed(pages)

		// Extract assistant preamble and final from execution pages
		ts.Assistant = extractAssistantState(pages)
	}

	return ts
}

func canonicalTurnStatus(turn *convstore.Turn) TurnStatus {
	if strings.TrimSpace(turn.Status) == "" {
		return TurnStatusCompleted
	}
	switch strings.ToLower(strings.TrimSpace(turn.Status)) {
	case "running":
		return TurnStatusRunning
	case "waiting_for_user":
		return TurnStatusWaitingForUser
	case "completed", "succeeded":
		return TurnStatusCompleted
	case "failed":
		return TurnStatusFailed
	case "canceled", "cancelled":
		return TurnStatusCanceled
	default:
		return TurnStatusCompleted
	}
}

func buildElicitationState(msg *agconv.MessageView) *ElicitationState {
	if msg == nil || msg.ElicitationId == nil {
		return nil
	}
	es := &ElicitationState{
		ElicitationID: strings.TrimSpace(*msg.ElicitationId),
		Message:       stringValue(msg.Content),
	}
	// Map message status to elicitation status
	if msg.Status != nil {
		switch strings.ToLower(strings.TrimSpace(*msg.Status)) {
		case "pending":
			es.Status = ElicitationStatusPending
		case "accepted":
			es.Status = ElicitationStatusAccepted
		case "declined", "rejected":
			es.Status = ElicitationStatusDeclined
		case "canceled", "cancelled":
			es.Status = ElicitationStatusCanceled
		default:
			es.Status = ElicitationStatusPending
		}
	} else {
		es.Status = ElicitationStatusPending
	}
	// Extract requestedSchema and callbackUrl from the assistant message content,
	// which is where the elicitation request payload is stored. The
	// UserElicitationData field (elicitation_payload_id) holds the user's
	// response payload, not the original request.
	if content := stringValue(msg.Content); content != "" {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(content), &payload) == nil {
			if schema, ok := payload["requestedSchema"].(map[string]interface{}); ok {
				es.RequestedSchema = schema
			}
			if cb, ok := payload["callbackUrl"].(string); ok {
				es.CallbackURL = strings.TrimSpace(cb)
			}
		}
	}
	return es
}

func buildExecutionPages(turn *convstore.Turn) []*ExecutionPageState {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	parentToolMessages := indexToolMessagesByParentAndIteration(turn)
	var pages []*ExecutionPageState
	for _, message := range turn.Message {
		if message == nil || message.ModelCall == nil || isSummaryAssistantMessage(message) {
			continue
		}
		page := buildPageFromMessage(turn, message, parentToolMessages, len(pages))
		if page != nil {
			pages = append(pages, page)
		}
	}
	// Scan for a final assistant message that may not have a ModelCall
	// (e.g., created by the agent run loop's addMessage after model call completion).
	// Attach its content to the last page.
	if len(pages) > 0 {
		lastPage := pages[len(pages)-1]
		if !lastPage.FinalResponse || strings.TrimSpace(lastPage.Content) == "" {
			for i := len(turn.Message) - 1; i >= 0; i-- {
				msg := turn.Message[i]
				if msg == nil {
					continue
				}
				if isSummaryAssistantMessage(msg) {
					continue
				}
				role := strings.ToLower(strings.TrimSpace(msg.Role))
				if role != "assistant" || msg.Interim != 0 {
					continue
				}
				content := strings.TrimSpace(stringValue(msg.Content))
				if content == "" {
					continue
				}
				lastPage.Content = content
				lastPage.FinalResponse = true
				lastPage.FinalAssistantMessageID = msg.Id
				break
			}
		}
	}
	return pages
}

func buildPageFromMessage(turn *convstore.Turn, message *agconv.MessageView, indexed map[string][]*agconv.ToolMessageView, pageIdx int) *ExecutionPageState {
	if message == nil || message.ModelCall == nil {
		return nil
	}
	iteration := 0
	if message.Iteration != nil {
		iteration = *message.Iteration
	}
	page := &ExecutionPageState{
		PageID:             message.Id,
		AssistantMessageID: message.Id,
		ParentMessageID:    message.Id,
		TurnID:             stringValue(message.TurnId),
		Iteration:          iteration,
		Preamble:           executionPreamble(message),
		Content:            strings.TrimSpace(stringValue(message.Content)),
		FinalResponse:      isFinalExecutionMessage(message) && !isSummaryAssistantMessage(message),
		Status:             pageStatus(message),
	}

	// Build model step from ModelCall
	page.ModelSteps = []*ModelStepState{buildModelStep(message)}

	// Build tool steps from tool messages. Pass the parent message's
	// linked conversation ID so tool steps can inherit it.
	parentLinkedConvID := ""
	if message.LinkedConversationId != nil {
		parentLinkedConvID = strings.TrimSpace(*message.LinkedConversationId)
	}
	toolMessages, _ := collectToolChildren(turn, message, indexed)
	for _, tm := range toolMessages {
		if tm == nil || tm.ToolCall == nil {
			continue
		}
		ts := buildToolStep(tm)
		if ts != nil {
			// Attach linked conversation from parent assistant message
			if parentLinkedConvID != "" && ts.LinkedConversationID == "" {
				ts.LinkedConversationID = parentLinkedConvID
			}
			page.ToolSteps = append(page.ToolSteps, ts)
		}
	}

	// Set preamble/final message IDs
	if page.Preamble != "" {
		page.PreambleMessageID = message.Id
	}
	if page.FinalResponse {
		page.FinalAssistantMessageID = message.Id
	}

	return page
}

func pageStatus(message *agconv.MessageView) string {
	if message == nil {
		return ""
	}
	status := strings.TrimSpace(stringValue(message.Status))
	if status == "" && message.ModelCall != nil {
		status = strings.TrimSpace(message.ModelCall.Status)
	}
	return status
}

func buildModelStep(message *agconv.MessageView) *ModelStepState {
	if message == nil || message.ModelCall == nil {
		return nil
	}
	mc := message.ModelCall
	step := &ModelStepState{
		ModelCallID:        message.Id,
		AssistantMessageID: message.Id,
		Provider:           strings.TrimSpace(mc.Provider),
		Model:              strings.TrimSpace(mc.Model),
		Status:             strings.TrimSpace(mc.Status),
		StartedAt:          mc.StartedAt,
		CompletedAt:        mc.CompletedAt,
	}
	if mc.RequestPayloadId != nil {
		step.RequestPayloadID = strings.TrimSpace(*mc.RequestPayloadId)
	}
	if mc.ResponsePayloadId != nil {
		step.ResponsePayloadID = strings.TrimSpace(*mc.ResponsePayloadId)
	}
	if mc.ProviderRequestPayloadId != nil {
		step.ProviderRequestPayloadID = strings.TrimSpace(*mc.ProviderRequestPayloadId)
	}
	if mc.ProviderResponsePayloadId != nil {
		step.ProviderResponsePayloadID = strings.TrimSpace(*mc.ProviderResponsePayloadId)
	}
	if mc.StreamPayloadId != nil {
		step.StreamPayloadID = strings.TrimSpace(*mc.StreamPayloadId)
	}
	if mc.ModelCallRequestPayload != nil {
		step.RequestPayload = mc.ModelCallRequestPayload
	}
	if mc.ModelCallResponsePayload != nil {
		step.ResponsePayload = mc.ModelCallResponsePayload
	}
	if mc.ModelCallProviderRequestPayload != nil {
		step.ProviderRequestPayload = mc.ModelCallProviderRequestPayload
	}
	if mc.ModelCallProviderResponsePayload != nil {
		step.ProviderResponsePayload = mc.ModelCallProviderResponsePayload
	}
	if mc.ModelCallStreamPayload != nil {
		step.StreamPayload = mc.ModelCallStreamPayload
	}
	return step
}

func buildToolStep(tm *agconv.ToolMessageView) *ToolStepState {
	if tm == nil || tm.ToolCall == nil {
		return nil
	}
	tc := tm.ToolCall
	step := &ToolStepState{
		ToolCallID:    strings.TrimSpace(tc.OpId),
		ToolMessageID: strings.TrimSpace(tm.Id),
		ToolName:      strings.TrimSpace(tc.ToolName),
		Status:        strings.TrimSpace(tc.Status),
		StartedAt:     tc.StartedAt,
		CompletedAt:   tc.CompletedAt,
	}
	if tc.RequestPayloadId != nil {
		step.RequestPayloadID = strings.TrimSpace(*tc.RequestPayloadId)
	}
	if tc.ResponsePayloadId != nil {
		step.ResponsePayloadID = strings.TrimSpace(*tc.ResponsePayloadId)
	}
	if tc.RequestPayload != nil {
		step.RequestPayload = tc.RequestPayload
	}
	if tc.ResponsePayload != nil {
		step.ResponsePayload = tc.ResponsePayload
	}
	if tm.LinkedConversationId != nil {
		step.LinkedConversationID = strings.TrimSpace(*tm.LinkedConversationId)
	}
	return step
}

func appendLinkedConversationState(turn *TurnState, seen map[string]struct{}, linked *LinkedConversationState) {
	if turn == nil || linked == nil {
		return
	}
	id := strings.TrimSpace(linked.ConversationID)
	if id == "" {
		return
	}
	if _, ok := seen[id]; ok {
		return
	}
	seen[id] = struct{}{}
	turn.LinkedConversations = append(turn.LinkedConversations, linked)
}

func toolCallIDFromToolMessage(tm *agconv.ToolMessageView) string {
	if tm == nil || tm.ToolCall == nil {
		return ""
	}
	return strings.TrimSpace(tm.ToolCall.OpId)
}

func firstToolCallID(items []*agconv.ToolMessageView) string {
	for _, item := range items {
		if id := toolCallIDFromToolMessage(item); id != "" {
			return id
		}
	}
	return ""
}

func extractAssistantState(pages []*ExecutionPageState) *AssistantState {
	if len(pages) == 0 {
		return nil
	}
	as := &AssistantState{}
	// First page with preamble becomes the preamble
	for _, p := range pages {
		if p.Preamble != "" && as.Preamble == nil {
			as.Preamble = &AssistantMessageState{
				MessageID: p.PreambleMessageID,
				Content:   p.Preamble,
			}
			break
		}
	}
	// Last page with final content becomes the final
	for i := len(pages) - 1; i >= 0; i-- {
		p := pages[i]
		if p == nil || p.Iteration == 0 {
			continue
		}
		if p.FinalResponse && p.Content != "" {
			as.Final = &AssistantMessageState{
				MessageID: p.FinalAssistantMessageID,
				Content:   p.Content,
			}
			break
		}
	}
	if as.Preamble == nil && as.Final == nil {
		return nil
	}
	return as
}

func isSummaryAssistantMessage(message *agconv.MessageView) bool {
	if message == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(message.Role)) != "assistant" {
		return false
	}
	if message.Mode != nil && strings.EqualFold(strings.TrimSpace(*message.Mode), "summary") {
		return true
	}
	if message.Status != nil && strings.EqualFold(strings.TrimSpace(*message.Status), "summary") {
		return true
	}
	return false
}

func calcTotalElapsed(pages []*ExecutionPageState) int64 {
	if len(pages) == 0 {
		return 0
	}
	var earliest, latest time.Time
	for _, p := range pages {
		for _, ms := range p.ModelSteps {
			if ms == nil {
				continue
			}
			if ms.StartedAt != nil && (earliest.IsZero() || ms.StartedAt.Before(earliest)) {
				earliest = *ms.StartedAt
			}
			if ms.CompletedAt != nil && (latest.IsZero() || ms.CompletedAt.After(latest)) {
				latest = *ms.CompletedAt
			}
		}
		for _, ts := range p.ToolSteps {
			if ts == nil {
				continue
			}
			if ts.CompletedAt != nil && (latest.IsZero() || ts.CompletedAt.After(latest)) {
				latest = *ts.CompletedAt
			}
		}
	}
	if earliest.IsZero() || latest.IsZero() {
		return 0
	}
	return latest.Sub(earliest).Milliseconds()
}
