package sdk

import "encoding/json"

// Selectors derive renderable state from the canonical ConversationState.
// UI layers should only render what these selectors return — never synthesize
// execution structure from raw transcript or stream data.

// SelectRenderTurns returns all turns suitable for rendering.
// It filters out turns that have no meaningful content to display.
func SelectRenderTurns(state *ConversationState) []*TurnState {
	if state == nil {
		return nil
	}
	out := make([]*TurnState, 0, len(state.Turns))
	for _, turn := range state.Turns {
		if turn == nil {
			continue
		}
		// Include turns that have at least a user message, execution, assistant content, or elicitation
		if turn.User != nil || turn.Execution != nil || turn.Assistant != nil || turn.Elicitation != nil || turn.Status == TurnStatusRunning {
			out = append(out, turn)
		}
	}
	return out
}

// SelectVisibleExecutionPage returns the execution page at the given index.
// If pageIndex is out of range, returns the active (latest) page.
// Returns nil if the turn has no execution state.
func SelectVisibleExecutionPage(turn *TurnState, pageIndex int) *ExecutionPageState {
	if turn == nil || turn.Execution == nil || len(turn.Execution.Pages) == 0 {
		return nil
	}
	pages := turn.Execution.Pages
	if pageIndex < 0 || pageIndex >= len(pages) {
		pageIndex = turn.Execution.ActivePageIdx
	}
	if pageIndex < 0 || pageIndex >= len(pages) {
		return pages[len(pages)-1]
	}
	return pages[pageIndex]
}

// AssistantBubble is the content to render as the assistant's visible response.
type AssistantBubble struct {
	MessageID string
	Content   string
	IsFinal   bool
}

// SelectAssistantBubble determines what assistant content to display for a turn.
// Rules (from the proposal):
//  1. Exactly one assistant bubble per visible execution page.
//  2. If visible page has final content, show final content.
//  3. Else if visible page has narration, show narration.
func SelectAssistantBubble(turn *TurnState, pageIndex int) *AssistantBubble {
	if turn == nil {
		return nil
	}
	// First check turn-level assistant state
	if turn.Assistant != nil && turn.Assistant.Final != nil && turn.Assistant.Final.Content != "" {
		return &AssistantBubble{
			MessageID: turn.Assistant.Final.MessageID,
			Content:   turn.Assistant.Final.Content,
			IsFinal:   true,
		}
	}
	// Fallback to page-level content
	page := SelectVisibleExecutionPage(turn, pageIndex)
	if page != nil {
		if page.FinalResponse && page.Content != "" {
			return &AssistantBubble{
				MessageID: page.FinalAssistantMessageID,
				Content:   page.Content,
				IsFinal:   true,
			}
		}
		if page.Narration != "" {
			return &AssistantBubble{
				MessageID: page.NarrationMessageID,
				Content:   page.Narration,
				IsFinal:   false,
			}
		}
	}
	// Fallback to turn-level narration
	if turn.Assistant != nil && turn.Assistant.Narration != nil && turn.Assistant.Narration.Content != "" {
		return &AssistantBubble{
			MessageID: turn.Assistant.Narration.MessageID,
			Content:   turn.Assistant.Narration.Content,
			IsFinal:   false,
		}
	}
	return nil
}

// ElicitationDialog describes the elicitation UI to present to the user.
type ElicitationDialog struct {
	ElicitationID   string
	Status          ElicitationStatus
	Message         string
	RequestedSchema json.RawMessage
	CallbackURL     string
	ResponsePayload json.RawMessage
	// IsPending is true when the user needs to respond.
	IsPending bool
}

// SelectElicitationDialog returns the elicitation dialog state for a turn,
// or nil if no elicitation is active.
//
// Rules (from the proposal):
//   - Show schema form in a dialog
//   - Do not render duplicate inline form or duplicate assistant bubble with
//     the same elicitation text
func SelectElicitationDialog(turn *TurnState) *ElicitationDialog {
	if turn == nil || turn.Elicitation == nil {
		return nil
	}
	e := turn.Elicitation
	return &ElicitationDialog{
		ElicitationID:   e.ElicitationID,
		Status:          e.Status,
		Message:         e.Message,
		RequestedSchema: e.RequestedSchema,
		CallbackURL:     e.CallbackURL,
		ResponsePayload: e.ResponsePayload,
		IsPending:       e.Status == ElicitationStatusPending,
	}
}

// SelectLinkedConversations returns linked child conversations for a turn.
func SelectLinkedConversations(turn *TurnState) []*LinkedConversationState {
	if turn == nil {
		return nil
	}
	return turn.LinkedConversations
}

// SelectLinkedConversationForToolCall returns the linked conversation
// associated with a specific tool call, or nil if none exists.
// This is the single source of truth for tool→child navigation.
func SelectLinkedConversationForToolCall(turn *TurnState, toolCallID string) *LinkedConversationState {
	if turn == nil || toolCallID == "" {
		return nil
	}
	for _, lc := range turn.LinkedConversations {
		if lc.ToolCallID == toolCallID {
			return lc
		}
	}
	return nil
}

// SelectExecutionPageCount returns the total number of execution pages (iterations) in a turn.
func SelectExecutionPageCount(turn *TurnState) int {
	if turn == nil || turn.Execution == nil {
		return 0
	}
	return len(turn.Execution.Pages)
}

// SelectTotalElapsedMs returns the total elapsed time for execution in a turn.
func SelectTotalElapsedMs(turn *TurnState) int64 {
	if turn == nil || turn.Execution == nil {
		return 0
	}
	return turn.Execution.TotalElapsedMs
}
