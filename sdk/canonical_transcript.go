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
	tmpState := &ConversationState{}
	ts := findOrCreateTurn(tmpState, strings.TrimSpace(turn.Id), canonicalTurnStatus(turn), turn.CreatedAt)
	if ts == nil {
		return nil
	}
	// Queue metadata
	if turn.StartedByMessageId != nil {
		ts.StartedByMessageID = strings.TrimSpace(*turn.StartedByMessageId)
	}
	// Extract user message, assistant messages, elicitation, linked conversations
	for _, msg := range turn.Message {
		if msg == nil {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "user":
			content := stringValue(msg.RawContent)
			if content == "" {
				content = stringValue(msg.Content)
			}
			if ts.User == nil && msg.Interim == 0 {
				ts.User = &UserMessageState{
					MessageID: msg.Id,
					Content:   content,
				}
			} else if msg.Interim == 0 && strings.TrimSpace(content) != "" {
				ts.Messages = append(ts.Messages, &TurnMessageState{
					MessageID: msg.Id,
					Role:      "user",
					Content:   content,
					CreatedAt: msg.CreatedAt,
					Sequence:  intValue(msg.Sequence),
					Interim:   msg.Interim,
					Mode:      strings.TrimSpace(stringValue(msg.Mode)),
					Status:    strings.TrimSpace(stringValue(msg.Status)),
				})
			}
		case "assistant":
			if msg.ModelCall == nil &&
				!isSummaryAssistantMessage(msg) &&
				msg.Interim == 0 &&
				!(msg.Mode != nil && strings.EqualFold(strings.TrimSpace(*msg.Mode), "exec")) {
				if content := visibleContentOrEmpty(msg.Content); content != "" {
					ts.Messages = append(ts.Messages, &TurnMessageState{
						MessageID: msg.Id,
						Role:      "assistant",
						Content:   content,
						CreatedAt: msg.CreatedAt,
						Sequence:  intValue(msg.Sequence),
						Interim:   msg.Interim,
						Mode:      strings.TrimSpace(stringValue(msg.Mode)),
						Status:    strings.TrimSpace(stringValue(msg.Status)),
					})
				}
			}
		}
		// Collect elicitation state
		if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
			setElicitationState(ts, buildElicitationState(msg))
		}
		// Collect linked conversations from messages
		if msg.LinkedConversationId != nil && strings.TrimSpace(*msg.LinkedConversationId) != "" {
			attachLinkedConversation(ts, &LinkedConversationState{
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
			attachLinkedConversation(ts, &LinkedConversationState{
				ConversationID: strings.TrimSpace(*tm.LinkedConversationId),
				CreatedAt:      tm.CreatedAt,
				ParentTurnID:   stringValue(msg.TurnId),
				ToolCallID:     toolCallIDFromToolMessage(tm),
			})
		}
	}

	// Build execution state from messages with model calls
	pages := buildExecutionPages(ts, turn)
	if len(pages) > 0 {
		if ts.Execution == nil {
			ts.Execution = &ExecutionState{}
		}
		ts.Execution.Pages = pages
		ts.Execution.ActivePageIdx = len(pages) - 1
		ts.Execution.TotalElapsedMs = calcTotalElapsed(pages)

		// Extract assistant narration and final from execution pages
		ts.Assistant = extractAssistantState(pages)
	}
	if latestNarration := latestTranscriptAssistantNarration(turn.Message); latestNarration != nil {
		if ts.Assistant == nil {
			ts.Assistant = &AssistantState{}
		}
		ts.Assistant.Narration = latestNarration
	}

	return ts
}

func canonicalTurnStatus(turn *convstore.Turn) TurnStatus {
	return turnStatusFromString(turn.Status, TurnStatusCompleted)
}

func buildElicitationState(msg *agconv.MessageView) *ElicitationState {
	if msg == nil || msg.ElicitationId == nil {
		return nil
	}
	es := &ElicitationState{
		ElicitationID: strings.TrimSpace(*msg.ElicitationId),
	}
	// Resolve elicitation message text. Prefer RawContent, fall back to Content.
	es.Message = stringValue(msg.Content)

	// Map message status to elicitation status
	if msg.Status != nil {
		es.Status = elicitationStatusForString(*msg.Status, ElicitationStatusPending)
	} else {
		es.Status = ElicitationStatusPending
	}

	// Use enriched elicitation map if available (populated by enrichTranscriptElicitations).
	// This avoids parsing requestedSchema from embedded content JSON.
	if msg.Elicitation != nil {
		if schema, ok := msg.Elicitation["requestedSchema"]; ok {
			es.RequestedSchema = marshalToRawJSON(schema)
		}
		if cb, ok := msg.Elicitation["callbackUrl"].(string); ok {
			es.CallbackURL = strings.TrimSpace(cb)
		}
		// Use the human-readable message from the elicitation map if available
		if message, ok := msg.Elicitation["message"].(string); ok {
			if m := strings.TrimSpace(message); m != "" {
				es.Message = m
			}
		}
		return es
	}

	// Fall back: extract requestedSchema and callbackUrl from embedded JSON in content.
	// This path handles legacy records where elicitation payload was stored in content.
	if content := stringValue(msg.Content); content != "" {
		var payload map[string]interface{}
		if json.Unmarshal([]byte(content), &payload) == nil {
			if schema, ok := payload["requestedSchema"]; ok {
				es.RequestedSchema = marshalToRawJSON(schema)
			}
			if cb, ok := payload["callbackUrl"].(string); ok {
				es.CallbackURL = strings.TrimSpace(cb)
			}
			// Use message from payload if content looks like raw JSON
			if message, ok := payload["message"].(string); ok {
				if m := strings.TrimSpace(message); m != "" {
					es.Message = m
				}
			}
		}
	}
	return es
}

func buildExecutionPages(ts *TurnState, turn *convstore.Turn) []*ExecutionPageState {
	if ts == nil || turn == nil || len(turn.Message) == 0 {
		return nil
	}
	parentToolMessages := indexToolMessagesByParentAndIteration(turn)
	for _, message := range turn.Message {
		if message == nil {
			continue
		}
		switch {
		case message.ModelCall != nil:
			_ = buildPageFromMessage(ts, turn, message, parentToolMessages)
		case isNarratorAssistantMessage(message):
			_ = buildNarratorPageFromMessage(ts, message)
		}
	}
	if ts.Execution == nil {
		return nil
	}
	pages := ts.Execution.Pages
	// Scan for a final assistant message that may not have a ModelCall
	// (e.g., created by the agent run loop's addMessage after model call completion).
	// Attach its content to the last page.
	if len(pages) > 0 {
		lastPage := pages[len(pages)-1]
		for i := len(turn.Message) - 1; i >= 0; i-- {
			msg := turn.Message[i]
			if msg == nil {
				continue
			}
			if !isPrimaryAssistantFinalMessage(msg) {
				continue
			}
			content := visibleContentOrEmpty(msg.Content)
			if content == "" {
				continue
			}
			lastPage.Sequence = intValue(msg.Sequence)
			lastPage.Content = content
			lastPage.FinalResponse = true
			lastPage.FinalAssistantMessageID = msg.Id
			break
		}
	}
	return pages
}

func isNarratorAssistantMessage(message *agconv.MessageView) bool {
	if message == nil {
		return false
	}
	if strings.ToLower(strings.TrimSpace(message.Role)) != "assistant" {
		return false
	}
	if message.Interim == 0 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(stringValue(message.Mode)), "narrator")
}

func buildNarratorPageFromMessage(ts *TurnState, message *agconv.MessageView) *ExecutionPageState {
	if ts == nil || message == nil {
		return nil
	}
	text := strings.TrimSpace(ptrString(message.Narration))
	if text == "" {
		text = visibleContentOrEmpty(message.Content)
	}
	if text == "" {
		return nil
	}
	page := findOrCreatePage(ts, message.Id, intValue(message.Iteration), strings.TrimSpace(stringValue(message.Mode)))
	if page == nil {
		return nil
	}
	page.PageID = message.Id
	page.AssistantMessageID = message.Id
	page.ParentMessageID = message.Id
	page.TurnID = stringValue(message.TurnId)
	page.Iteration = intValue(message.Iteration)
	page.Sequence = intValue(message.Sequence)
	page.Mode = strings.TrimSpace(stringValue(message.Mode))
	page.ExecutionRole = "narrator"
	page.Narration = text
	page.Content = text
	page.NarrationMessageID = message.Id
	page.Status = stepStatusFromString(strings.TrimSpace(stringValue(message.Status)), "running")
	if page.Status == "" {
		page.Status = "running"
	}
	payload := marshalToRawJSON(map[string]interface{}{
		"content": text,
	})
	step := &ModelStepState{
		ModelCallID:        strings.TrimSpace(message.Id),
		AssistantMessageID: strings.TrimSpace(message.Id),
		ExecutionRole:      "narrator",
		Status:             page.Status,
		ResponsePayload:    payload,
	}
	if !message.CreatedAt.IsZero() {
		startedAt := message.CreatedAt
		step.StartedAt = &startedAt
	}
	existing := upsertModelStep(page, step.ModelCallID)
	*existing = *step
	refreshExecutionRole(page)
	return page
}

func isPrimaryAssistantFinalMessage(message *agconv.MessageView) bool {
	if message == nil {
		return false
	}
	if isSummaryAssistantMessage(message) {
		return false
	}
	if strings.ToLower(strings.TrimSpace(message.Role)) != "assistant" || message.Interim != 0 {
		return false
	}
	if message.Mode != nil && strings.EqualFold(strings.TrimSpace(*message.Mode), "exec") {
		return false
	}
	return true
}

func buildPageFromMessage(ts *TurnState, turn *convstore.Turn, message *agconv.MessageView, indexed map[string][]*agconv.ToolMessageView) *ExecutionPageState {
	if message == nil || message.ModelCall == nil {
		return nil
	}
	iteration := 0
	if message.Iteration != nil {
		iteration = *message.Iteration
	}
	mode := ""
	// Set mode for summary passes so UI can style them distinctly.
	if isSummaryAssistantMessage(message) {
		mode = "summary"
	}
	page := findOrCreatePage(ts, message.Id, iteration, mode)
	if page == nil {
		return nil
	}
	page.PageID = message.Id
	page.AssistantMessageID = message.Id
	page.ParentMessageID = message.Id
	page.TurnID = stringValue(message.TurnId)
	page.Iteration = iteration
	page.Sequence = intValue(message.Sequence)
	page.Phase = strings.TrimSpace(stringValue(message.Phase))
	page.ExecutionRole = executionRoleFromSignals(page.ExecutionRole, page.Phase, page.Mode, "")
	if narration := executionNarration(message); narration != "" {
		page.Narration = narration
	}
	if content := visibleContentOrEmpty(message.Content); content != "" {
		page.Content = content
	}
	if isFinalExecutionMessage(message) && !isSummaryAssistantMessage(message) {
		page.FinalResponse = true
	}
	page.Status = pageStatus(message)
	if mode != "" {
		page.Mode = mode
		page.FinalResponse = false // summary is not the final user-facing response
	}

	// Build model step using shared upsert helper (enforces dedup semantics)
	if ms := buildModelStep(message); ms != nil {
		existing := upsertModelStep(page, ms.ModelCallID)
		*existing = *ms
	}

	// Build tool steps using shared upsert helper
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
		if ts == nil {
			continue
		}
		if parentLinkedConvID != "" && ts.LinkedConversationID == "" {
			ts.LinkedConversationID = parentLinkedConvID
		}
		existing := upsertToolStep(page, ts.ToolCallID)
		*existing = mergeToolStepState(existing, ts)
	}
	deriveExecutionPagePhase(page)
	refreshExecutionRole(page)

	// Set narration/final message IDs
	if page.Narration != "" {
		page.NarrationMessageID = message.Id
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
	return stepStatusFromString(status, "")
}

func buildModelStep(message *agconv.MessageView) *ModelStepState {
	if message == nil || message.ModelCall == nil {
		return nil
	}
	mc := message.ModelCall
	step := &ModelStepState{
		ModelCallID:        strings.TrimSpace(stringValue(mc.TraceId)),
		AssistantMessageID: message.Id,
		Phase:              strings.TrimSpace(stringValue(message.Phase)),
		Provider:           strings.TrimSpace(mc.Provider),
		Model:              strings.TrimSpace(mc.Model),
		Status:             stepStatusFromString(mc.Status, ""),
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
		step.RequestPayload = marshalToRawJSON(mc.ModelCallRequestPayload)
	}
	if mc.ModelCallResponsePayload != nil {
		step.ResponsePayload = marshalToRawJSON(mc.ModelCallResponsePayload)
	}
	if mc.ModelCallProviderRequestPayload != nil {
		step.ProviderRequestPayload = marshalToRawJSON(mc.ModelCallProviderRequestPayload)
	}
	if mc.ModelCallProviderResponsePayload != nil {
		step.ProviderResponsePayload = marshalToRawJSON(mc.ModelCallProviderResponsePayload)
	}
	if mc.ModelCallStreamPayload != nil {
		step.StreamPayload = marshalToRawJSON(mc.ModelCallStreamPayload)
	}
	step.ExecutionRole = executionRoleFromSignals(step.ExecutionRole, step.Phase, "", "", step.RequestPayload, step.ProviderRequestPayload, step.ResponsePayload, step.ProviderResponsePayload, step.StreamPayload)
	return step
}

func buildToolStep(tm *agconv.ToolMessageView) *ToolStepState {
	if tm == nil || tm.ToolCall == nil {
		return nil
	}
	tc := tm.ToolCall
	step := &ToolStepState{
		ToolCallID:    normalizeCanonicalToolCallID(tc.OpId),
		ToolMessageID: strings.TrimSpace(tm.Id),
		ToolName:      strings.TrimSpace(tc.ToolName),
		Content:       visibleContentOrEmpty(tm.Content),
		Status:        stepStatusFromString(tc.Status, ""),
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
		step.RequestPayload = marshalToRawJSON(tc.RequestPayload)
	}
	if tc.ResponsePayload != nil {
		step.ResponsePayload = marshalToRawJSON(tc.ResponsePayload)
	}
	if tm.LinkedConversationId != nil {
		step.LinkedConversationID = strings.TrimSpace(*tm.LinkedConversationId)
	}
	attachTranscriptAsyncOperation(step)
	step.ExecutionRole = executionRoleFromSignals(step.ExecutionRole, "", "", step.ToolName, step.RequestPayload, step.ResponsePayload)
	return step
}

func normalizeCanonicalToolCallID(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, ":approved") {
		return strings.TrimSpace(strings.TrimSuffix(value, ":approved"))
	}
	return value
}

func mergeToolStepState(existing, incoming *ToolStepState) ToolStepState {
	if existing == nil && incoming == nil {
		return ToolStepState{}
	}
	if existing == nil {
		return *incoming
	}
	if incoming == nil {
		return *existing
	}
	merged := *existing
	if strings.TrimSpace(incoming.ToolCallID) != "" {
		merged.ToolCallID = incoming.ToolCallID
	}
	if strings.TrimSpace(incoming.ToolMessageID) != "" && toolStepStatusRank(incoming.Status) >= toolStepStatusRank(existing.Status) {
		merged.ToolMessageID = incoming.ToolMessageID
	}
	if strings.TrimSpace(incoming.ToolName) != "" {
		merged.ToolName = incoming.ToolName
	}
	if role := normalizeExecutionRole(incoming.ExecutionRole); role != "" {
		merged.ExecutionRole = role
	}
	if toolStepStatusRank(incoming.Status) >= toolStepStatusRank(existing.Status) && strings.TrimSpace(incoming.Status) != "" {
		merged.Status = incoming.Status
	}
	if incoming.StartedAt != nil {
		merged.StartedAt = incoming.StartedAt
	}
	if incoming.CompletedAt != nil {
		merged.CompletedAt = incoming.CompletedAt
	}
	if strings.TrimSpace(incoming.Content) != "" {
		merged.Content = incoming.Content
	}
	if strings.TrimSpace(incoming.RequestPayloadID) != "" {
		merged.RequestPayloadID = incoming.RequestPayloadID
	}
	if strings.TrimSpace(incoming.ResponsePayloadID) != "" {
		merged.ResponsePayloadID = incoming.ResponsePayloadID
	}
	if incoming.RequestPayload != nil {
		merged.RequestPayload = incoming.RequestPayload
	}
	if incoming.ResponsePayload != nil {
		merged.ResponsePayload = incoming.ResponsePayload
	}
	if strings.TrimSpace(incoming.LinkedConversationID) != "" {
		merged.LinkedConversationID = incoming.LinkedConversationID
	}
	if strings.TrimSpace(incoming.OperationID) != "" {
		merged.OperationID = incoming.OperationID
	}
	if incoming.AsyncOperation != nil {
		merged.AsyncOperation = incoming.AsyncOperation
	}
	return merged
}

func attachTranscriptAsyncOperation(step *ToolStepState) {
	if step == nil {
		return
	}
	opID := strings.TrimSpace(step.OperationID)
	if opID == "" {
		switch {
		case strings.HasPrefix(strings.TrimSpace(step.ToolName), "llm/agents"):
			opID = strings.TrimSpace(step.LinkedConversationID)
			if opID == "" {
				if value := rawJSONFieldString(step.ResponsePayload, "conversationId"); value != "" {
					opID = value
				}
			}
		case strings.HasPrefix(strings.TrimSpace(step.ToolName), "system/exec"):
			if value := rawJSONFieldString(step.ResponsePayload, "sessionId"); value != "" {
				opID = value
			}
		}
	}
	if opID == "" {
		return
	}
	step.OperationID = opID
	if step.AsyncOperation == nil {
		step.AsyncOperation = &AsyncOperationState{OperationID: opID}
	}
	if step.AsyncOperation.Status == "" {
		step.AsyncOperation.Status = strings.TrimSpace(step.Status)
	}
	if step.AsyncOperation.Response == nil && step.ResponsePayload != nil {
		step.AsyncOperation.Response = step.ResponsePayload
	}
}

func rawJSONFieldString(raw json.RawMessage, field string) string {
	if len(raw) == 0 || strings.TrimSpace(field) == "" {
		return ""
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	for _, key := range []string{"inlineBody", "InlineBody"} {
		if inline, ok := obj[key].(string); ok && strings.TrimSpace(inline) != "" {
			var nested map[string]interface{}
			if err := json.Unmarshal([]byte(inline), &nested); err == nil {
				obj = nested
			}
			break
		}
	}
	value, ok := obj[field]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(stringValueInterface(value))
}

func stringValueInterface(value interface{}) string {
	switch actual := value.(type) {
	case string:
		return actual
	default:
		return strings.TrimSpace(strings.ReplaceAll(string(marshalToRawJSON(actual)), "\"", ""))
	}
}

func toolStepStatusRank(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "planned", "pending":
		return 1
	case "running", "streaming", "waiting_for_user", "blocked", "in_progress":
		return 2
	case "completed", "succeeded", "success", "done", "failed", "error", "rejected", "canceled", "cancelled", "terminated":
		return 3
	default:
		return 0
	}
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
	// First page with narration becomes the narration
	for _, p := range pages {
		if p.Narration != "" && as.Narration == nil {
			as.Narration = &AssistantMessageState{
				MessageID: p.NarrationMessageID,
				Content:   p.Narration,
			}
			break
		}
	}
	// Last page with final content becomes the final
	for i := len(pages) - 1; i >= 0; i-- {
		p := pages[i]
		if p == nil || p.Mode == "summary" {
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
	if as.Narration == nil && as.Final == nil {
		return nil
	}
	return as
}

func latestTranscriptAssistantNarration(messages []*agconv.MessageView) *AssistantMessageState {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}
		if isSummaryAssistantMessage(msg) {
			continue
		}
		if strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" || msg.Interim == 0 {
			continue
		}
		if text := strings.TrimSpace(ptrString(msg.Narration)); text != "" {
			return &AssistantMessageState{
				MessageID: msg.Id,
				Content:   text,
			}
		}
		if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
			return &AssistantMessageState{
				MessageID: msg.Id,
				Content:   text,
			}
		}
	}
	return nil
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
