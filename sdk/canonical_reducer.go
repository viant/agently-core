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
		return reduceTurnTerminal(state, event, terminalStatusForEventType(event.Type))
	case streaming.EventTypeTurnFailed:
		return reduceTurnTerminal(state, event, terminalStatusForEventType(event.Type))
	case streaming.EventTypeTurnCanceled:
		return reduceTurnTerminal(state, event, terminalStatusForEventType(event.Type))
	case streaming.EventTypeTurnQueued:
		return reduceTurnQueued(state, event)

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

	// Tool calls planned (from reactor, before tool execution begins)
	case streaming.EventTypeToolCallsPlanned:
		return reduceToolCallsPlanned(state, event)

	// Tool call lifecycle
	case streaming.EventTypeToolCallStarted:
		return reduceToolStarted(state, event)
	case streaming.EventTypeToolCallDelta:
		return reduceToolCallDelta(state, event)
	case streaming.EventTypeToolCallWaiting:
		return reduceToolAsyncUpdate(state, event)
	case streaming.EventTypeToolCallCompleted:
		return reduceToolCompleted(state, event)
	case streaming.EventTypeToolCallFailed:
		return reduceToolAsyncUpdate(state, event)
	case streaming.EventTypeToolCallCanceled:
		return reduceToolAsyncUpdate(state, event)

	// Elicitation
	case streaming.EventTypeElicitationRequested:
		return reduceElicitationRequested(state, event)
	case streaming.EventTypeElicitationResolved:
		return reduceElicitationResolved(state, event)

	// Linked conversation
	case streaming.EventTypeLinkedConversationAttached:
		return reduceLinkedConversation(state, event)

	// Tool feed lifecycle
	case streaming.EventTypeToolFeedActive:
		return reduceFeedActive(state, event)
	case streaming.EventTypeToolFeedInactive:
		return reduceFeedInactive(state, event)

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
	startedByMessageID := strings.TrimSpace(event.StartedByMessageID)
	if startedByMessageID == "" {
		startedByMessageID = strings.TrimSpace(event.UserMessageID)
	}
	// Avoid duplicate turns
	for _, t := range state.Turns {
		if t.TurnID == turnID {
			markTurnRunning(t)
			if startedByMessageID != "" {
				t.StartedByMessageID = startedByMessageID
			}
			if strings.TrimSpace(event.UserMessageID) != "" {
				if t.User == nil {
					t.User = &UserMessageState{}
				}
				t.User.MessageID = strings.TrimSpace(event.UserMessageID)
			}
			return state
		}
	}
	turn := &TurnState{
		TurnID:    turnID,
		Status:    TurnStatusRunning,
		CreatedAt: event.CreatedAt,
	}
	if startedByMessageID != "" {
		turn.StartedByMessageID = startedByMessageID
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
	finalizeTurn(turn, status)
	return state
}

func reduceTurnQueued(state *ConversationState, event *streaming.Event) *ConversationState {
	turnID := strings.TrimSpace(event.TurnID)
	if turnID == "" {
		return state
	}
	// If the turn already exists (e.g. arrived via transcript snapshot), update it.
	for _, t := range state.Turns {
		if t.TurnID == turnID {
			markTurnQueuedIfMutable(t)
			return state
		}
	}
	turn := &TurnState{
		TurnID:    turnID,
		Status:    TurnStatusQueued,
		CreatedAt: event.CreatedAt,
		QueueSeq:  event.QueueSeq,
	}
	if event.StartedByMessageID != "" {
		turn.StartedByMessageID = strings.TrimSpace(event.StartedByMessageID)
	}
	if event.UserMessageID != "" {
		turn.User = &UserMessageState{
			MessageID: strings.TrimSpace(event.UserMessageID),
		}
	}
	state.Turns = append(state.Turns, turn)
	return state
}

// --- model lifecycle ---

func reduceModelStarted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	modelCallID := strings.TrimSpace(event.ModelCallID)
	if modelCallID == "" {
		modelCallID = strings.TrimSpace(event.AssistantMessageID)
	}
	step := upsertModelStep(page, modelCallID)
	applyModelStart(step, event)
	return state
}

func reduceModelCompleted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	// Find and update the matching model step
	modelCallID := strings.TrimSpace(event.ModelCallID)
	if modelCallID == "" {
		modelCallID = strings.TrimSpace(event.AssistantMessageID)
	}
	for _, ms := range page.ModelSteps {
		if ms.ModelCallID == modelCallID {
			applyModelCompletion(ms, event)
			break
		}
	}
	applyModelResultToPage(page, event)
	return state
}

// --- tool calls planned (reactor fast-path) ---

func reduceToolCallsPlanned(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	if event.Content != "" {
		page.Content = event.Content
	}
	if event.Preamble != "" {
		page.Preamble = event.Preamble
	}
	// Seed preliminary tool steps so SDK consumers can show planned tools
	// immediately, before tool_call_started arrives from the database.
	for _, tc := range event.ToolCallsPlanned {
		toolCallID := strings.TrimSpace(tc.ToolCallID)
		toolName := strings.TrimSpace(tc.ToolName)
		if toolCallID == "" && toolName == "" {
			continue
		}
		step := upsertToolStep(page, toolCallID)
		applyPlannedToolStep(step, toolCallID, toolName)
	}
	return state
}

// --- assistant content ---

func reduceAssistantPreamble(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	setAssistantPreamble(turn, page, strings.TrimSpace(event.AssistantMessageID), event.Content)
	return state
}

func reduceAssistantFinal(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	setAssistantFinal(turn, page, strings.TrimSpace(event.AssistantMessageID), event.Content)
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
	for _, ms := range page.ModelSteps {
		ms.Status = modelStepStatusForEvent(event, ms.Status, ms.Status)
	}
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
	step := upsertToolStep(page, toolCallID)
	applyToolStart(step, event)
	return state
}

func reduceToolCompleted(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	ensureToolCompletion(page, event)
	return state
}

func reduceToolAsyncUpdate(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	page := ensureCurrentPage(turn, event)
	step := upsertToolStep(page, firstNonEmptyString(strings.TrimSpace(event.ToolCallID), strings.TrimSpace(event.OperationID)))
	if step.ToolName == "" {
		step.ToolName = strings.TrimSpace(event.ToolName)
	}
	if step.ToolMessageID == "" {
		step.ToolMessageID = strings.TrimSpace(event.ToolMessageID)
	}
	applyAsyncOperation(step, event)
	step.Status = stepStatusFromString(event.Status, step.Status)
	if event.Type == streaming.EventTypeToolCallCompleted || event.Type == streaming.EventTypeToolCallFailed || event.Type == streaming.EventTypeToolCallCanceled {
		step.CompletedAt = completedAtForEvent(event)
	}
	return state
}

// --- elicitation ---

func reduceElicitationRequested(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	applyElicitationRequested(turn, event)
	return state
}

func reduceElicitationResolved(state *ConversationState, event *streaming.Event) *ConversationState {
	turn := findOrCreateTurnWithTime(state, event)
	if turn == nil {
		return state
	}
	applyElicitationResolved(turn, event)
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
	attachLinkedConversation(turn, &LinkedConversationState{
		ConversationID:       linkedID,
		ParentConversationID: strings.TrimSpace(event.ConversationID),
		ParentTurnID:         strings.TrimSpace(event.TurnID),
		ToolCallID:           strings.TrimSpace(event.ToolCallID),
		AgentID:              strings.TrimSpace(event.LinkedConversationAgentID),
		Title:                strings.TrimSpace(event.LinkedConversationTitle),
		CreatedAt:            event.CreatedAt,
	})
	applyLinkedConversationToToolSteps(turn, event)
	return state
}

// --- tool feeds ---

func reduceFeedActive(state *ConversationState, event *streaming.Event) *ConversationState {
	if state == nil || event == nil {
		return state
	}
	feedID := strings.TrimSpace(event.FeedID)
	if feedID == "" {
		return state
	}
	activateFeed(state, &ActiveFeedState{
		FeedID:    feedID,
		Title:     strings.TrimSpace(event.FeedTitle),
		ItemCount: event.FeedItemCount,
		Data:      marshalToRawJSON(event.FeedData),
	})
	return state
}

func reduceFeedInactive(state *ConversationState, event *streaming.Event) *ConversationState {
	deactivateFeed(state, event.FeedID)
	return state
}
