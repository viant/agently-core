package sdk

import (
	"strings"

	"github.com/viant/agently-core/runtime/streaming"
)

// Reducer applies a stream event to a ConversationState, returning the
// updated state. The reducer is source-agnostic: both live stream events
// and transcript-derived events feed into the same function.
//
// The reducer is the single owner of state transitions. UI and SDK callers
// must not infer execution structure outside this function.
func Reduce(state *ConversationState, event *streaming.Event) *ConversationState {
	if event == nil {
		return state
	}
	if state == nil {
		state = &ConversationState{}
	}
	if state.ConversationID == "" {
		state.ConversationID = strings.TrimSpace(event.ConversationID)
	}

	switch event.Type {
	// Turn lifecycle
	case streaming.EventTypeTurnStarted:
		return reduceTurnStarted(state, event)
	case streaming.EventTypeTurnCompleted:
		return reduceTurnTerminal(state, event, TurnStatusCompleted)
	case streaming.EventTypeTurnFailed:
		return reduceTurnTerminal(state, event, TurnStatusFailed)
	case streaming.EventTypeTurnCanceled:
		return reduceTurnTerminal(state, event, TurnStatusCanceled)

	// Model lifecycle
	case streaming.EventTypeModelStarted:
		return reduceModelStarted(state, event)
	case streaming.EventTypeModelCompleted:
		return reduceModelCompleted(state, event)

	// Assistant content (aggregated)
	case streaming.EventTypeAssistantPreamble:
		return reduceAssistantPreamble(state, event)
	case streaming.EventTypeAssistantFinal:
		return reduceAssistantFinal(state, event)

	// Stream deltas — accumulate into current page
	case streaming.EventTypeTextDelta:
		return reduceTextDelta(state, event)
	case streaming.EventTypeReasoningDelta:
		return reduceReasoningDelta(state, event)

	// Tool call lifecycle
	case streaming.EventTypeToolCallStarted:
		return reduceToolStarted(state, event)
	case streaming.EventTypeToolCallDelta:
		return reduceToolCallDelta(state, event)
	case streaming.EventTypeToolCallCompleted:
		return reduceToolCompleted(state, event)

	// Elicitation
	case streaming.EventTypeElicitationRequested:
		return reduceElicitationRequested(state, event)
	case streaming.EventTypeElicitationResolved:
		return reduceElicitationResolved(state, event)

	// Linked conversation
	case streaming.EventTypeLinkedConversationAttached:
		return reduceLinkedConversation(state, event)

	// Metadata — usage, item_completed are no-ops for state
	case streaming.EventTypeUsage, streaming.EventTypeItemCompleted:
		return state
	}
	return state
}

// --- turn lifecycle ---

func reduceTurnStarted(state *ConversationState, event *streaming.Event) *ConversationState {
	turnID := strings.TrimSpace(event.TurnID)
	if turnID == "" {
		return state
	}
	// Avoid duplicate turns
	for _, t := range state.Turns {
		if t.TurnID == turnID {
			t.Status = TurnStatusRunning
			return state
		}
	}
	turn := &TurnState{
		TurnID:    turnID,
		Status:    TurnStatusRunning,
		CreatedAt: event.CreatedAt,
	}
	if event.UserMessageID != "" {
		turn.User = &UserMessageState{
			MessageID: strings.TrimSpace(event.UserMessageID),
		}
	}
	state.Turns = append(state.Turns, turn)
	return state
}

func reduceTurnTerminal(state *ConversationState, event *streaming.Event, status TurnStatus) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	// Don't downgrade a terminal status
	if turn.Status == TurnStatusFailed || turn.Status == TurnStatusCanceled {
		return state
	}
	turn.Status = status
	return state
}

// --- model lifecycle ---

func reduceModelStarted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	modelCallID := strings.TrimSpace(event.AssistantMessageID)
	// Dedup: don't append if a step with the same ModelCallID already exists
	for _, ms := range page.ModelSteps {
		if ms.ModelCallID == modelCallID {
			ms.Status = strings.TrimSpace(event.Status)
			return state
		}
	}
	step := &ModelStepState{
		ModelCallID:        modelCallID,
		AssistantMessageID: modelCallID,
		Status:             strings.TrimSpace(event.Status),
		StartedAt:          &event.CreatedAt,
	}
	if event.Model != nil {
		step.Provider = strings.TrimSpace(event.Model.Provider)
		step.Model = strings.TrimSpace(event.Model.Model)
	}
	if event.RequestPayloadID != "" {
		step.RequestPayloadID = strings.TrimSpace(event.RequestPayloadID)
	}
	page.ModelSteps = append(page.ModelSteps, step)
	return state
}

func reduceModelCompleted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	// Find and update the matching model step
	for _, ms := range page.ModelSteps {
		if ms.ModelCallID == strings.TrimSpace(event.AssistantMessageID) {
			ms.Status = strings.TrimSpace(event.Status)
			if event.ResponsePayloadID != "" {
				ms.ResponsePayloadID = strings.TrimSpace(event.ResponsePayloadID)
			}
			if event.CompletedAt != nil {
				ms.CompletedAt = event.CompletedAt
			} else {
				t := event.CreatedAt
				ms.CompletedAt = &t
			}
			break
		}
	}
	// Update page content from the event
	if content := strings.TrimSpace(event.Content); content != "" {
		page.Content = content
	}
	if preamble := strings.TrimSpace(event.Preamble); preamble != "" {
		page.Preamble = preamble
	}
	if event.FinalResponse {
		page.FinalResponse = true
		page.FinalAssistantMessageID = strings.TrimSpace(event.AssistantMessageID)
	}
	return state
}

// --- assistant content ---

func reduceAssistantPreamble(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	if turn.Assistant == nil {
		turn.Assistant = &AssistantState{}
	}
	turn.Assistant.Preamble = &AssistantMessageState{
		MessageID: strings.TrimSpace(event.AssistantMessageID),
		Content:   strings.TrimSpace(event.Content),
	}
	// Also set on current page
	page := ensureCurrentPage(turn, event)
	page.Preamble = strings.TrimSpace(event.Content)
	page.PreambleMessageID = strings.TrimSpace(event.AssistantMessageID)
	return state
}

func reduceAssistantFinal(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	if turn.Assistant == nil {
		turn.Assistant = &AssistantState{}
	}
	turn.Assistant.Final = &AssistantMessageState{
		MessageID: strings.TrimSpace(event.AssistantMessageID),
		Content:   strings.TrimSpace(event.Content),
	}
	// Also set on current page
	page := ensureCurrentPage(turn, event)
	page.Content = strings.TrimSpace(event.Content)
	page.FinalResponse = true
	page.FinalAssistantMessageID = strings.TrimSpace(event.AssistantMessageID)
	return state
}

// --- stream deltas ---

func reduceTextDelta(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	page.Content += event.Content
	return state
}

func reduceReasoningDelta(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	page.Preamble += event.Content
	return state
}

func reduceToolCallDelta(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	// Tool call deltas carry argument fragments; no state update needed
	// until tool_call_completed provides the full arguments.
	return state
}

// --- tool lifecycle ---

func reduceToolStarted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	toolCallID := strings.TrimSpace(event.ToolCallID)
	// Dedup: don't append if a step with the same ToolCallID already exists
	if toolCallID != "" {
		for _, ts := range page.ToolSteps {
			if ts.ToolCallID == toolCallID {
				ts.Status = strings.TrimSpace(event.Status)
				return state
			}
		}
	}
	step := &ToolStepState{
		ToolCallID:    toolCallID,
		ToolMessageID: strings.TrimSpace(event.ToolMessageID),
		ToolName:      strings.TrimSpace(event.ToolName),
		Status:        strings.TrimSpace(event.Status),
		StartedAt:     &event.CreatedAt,
	}
	if event.RequestPayloadID != "" {
		step.RequestPayloadID = strings.TrimSpace(event.RequestPayloadID)
	}
	page.ToolSteps = append(page.ToolSteps, step)
	return state
}

func reduceToolCompleted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	toolCallID := strings.TrimSpace(event.ToolCallID)
	completedAt := event.CompletedAt
	if completedAt == nil {
		t := event.CreatedAt
		completedAt = &t
	}
	for _, ts := range page.ToolSteps {
		if ts.ToolCallID == toolCallID {
			ts.Status = strings.TrimSpace(event.Status)
			if event.ResponsePayloadID != "" {
				ts.ResponsePayloadID = strings.TrimSpace(event.ResponsePayloadID)
			}
			if event.LinkedConversationID != "" {
				ts.LinkedConversationID = strings.TrimSpace(event.LinkedConversationID)
			}
			ts.CompletedAt = completedAt
			return state
		}
	}
	// Tool step not found — create it (transcript reconciliation case)
	step := &ToolStepState{
		ToolCallID:    toolCallID,
		ToolMessageID: strings.TrimSpace(event.ToolMessageID),
		ToolName:      strings.TrimSpace(event.ToolName),
		Status:        strings.TrimSpace(event.Status),
	}
	if event.ResponsePayloadID != "" {
		step.ResponsePayloadID = strings.TrimSpace(event.ResponsePayloadID)
	}
	if event.LinkedConversationID != "" {
		step.LinkedConversationID = strings.TrimSpace(event.LinkedConversationID)
	}
	step.CompletedAt = completedAt
	page.ToolSteps = append(page.ToolSteps, step)
	return state
}

// --- elicitation ---

func reduceElicitationRequested(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	turn.Status = TurnStatusWaitingForUser
	turn.Elicitation = &ElicitationState{
		ElicitationID:   strings.TrimSpace(event.ElicitationID),
		Status:          ElicitationStatusPending,
		Message:         strings.TrimSpace(event.Content),
		RequestedSchema: event.ElicitationData,
		CallbackURL:     strings.TrimSpace(event.CallbackURL),
	}
	return state
}

func reduceElicitationResolved(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	if turn.Elicitation == nil {
		turn.Elicitation = &ElicitationState{
			ElicitationID: strings.TrimSpace(event.ElicitationID),
		}
	}
	switch strings.ToLower(strings.TrimSpace(event.Status)) {
	case "accepted":
		turn.Elicitation.Status = ElicitationStatusAccepted
	case "declined", "rejected":
		turn.Elicitation.Status = ElicitationStatusDeclined
	case "canceled", "cancelled":
		turn.Elicitation.Status = ElicitationStatusCanceled
	default:
		turn.Elicitation.Status = ElicitationStatusDeclined
	}
	turn.Elicitation.ResponsePayload = event.ResponsePayload
	// Resume the turn
	if turn.Status == TurnStatusWaitingForUser {
		turn.Status = TurnStatusRunning
	}
	return state
}

// --- linked conversation ---

func reduceLinkedConversation(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	linkedID := strings.TrimSpace(event.LinkedConversationID)
	if linkedID == "" {
		return state
	}
	// Deduplicate
	for _, lc := range turn.LinkedConversations {
		if lc.ConversationID == linkedID {
			return state
		}
	}
	turn.LinkedConversations = append(turn.LinkedConversations, &LinkedConversationState{
		ConversationID:       linkedID,
		ParentConversationID: strings.TrimSpace(event.ConversationID),
		ParentTurnID:         strings.TrimSpace(event.TurnID),
		ToolCallID:           strings.TrimSpace(event.ToolCallID),
		CreatedAt:            event.CreatedAt,
	})
	// Also attach to the matching tool step if possible
	if toolCallID := strings.TrimSpace(event.ToolCallID); toolCallID != "" && turn.Execution != nil {
		for _, p := range turn.Execution.Pages {
			for _, ts := range p.ToolSteps {
				if ts.ToolCallID == toolCallID {
					ts.LinkedConversationID = linkedID
				}
			}
		}
	}
	return state
}

// --- helpers ---

func findOrCreateTurn(state *ConversationState, turnID string) *TurnState {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	for _, t := range state.Turns {
		if t.TurnID == turnID {
			return t
		}
	}
	turn := &TurnState{
		TurnID: turnID,
		Status: TurnStatusRunning,
	}
	state.Turns = append(state.Turns, turn)
	return turn
}

func findOrCreateTurnWithTime(state *ConversationState, event *streaming.Event) *TurnState {
	turn := findOrCreateTurn(state, event.TurnID)
	if turn != nil && turn.CreatedAt.IsZero() {
		turn.CreatedAt = event.CreatedAt
	}
	return turn
}

func ensureCurrentPage(turn *TurnState, event *streaming.Event) *ExecutionPageState {
	if turn.Execution == nil {
		turn.Execution = &ExecutionState{}
	}
	iteration := event.Iteration
	// Try to find an existing page for this iteration
	for _, p := range turn.Execution.Pages {
		if p.Iteration == iteration {
			return p
		}
	}
	page := &ExecutionPageState{
		PageID:             strings.TrimSpace(event.AssistantMessageID),
		AssistantMessageID: strings.TrimSpace(event.AssistantMessageID),
		ParentMessageID:    strings.TrimSpace(event.ParentMessageID),
		TurnID:             strings.TrimSpace(event.TurnID),
		Iteration:          iteration,
	}
	turn.Execution.Pages = append(turn.Execution.Pages, page)
	turn.Execution.ActivePageIdx = len(turn.Execution.Pages) - 1
	return page
}
