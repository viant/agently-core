package sdk

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
)

// marshalToRawJSON marshals v to json.RawMessage.
// Returns nil if v is nil or marshaling fails.
// Handles existing json.RawMessage and []byte inputs without re-encoding.
func marshalToRawJSON(v interface{}) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		if len(raw) == 0 {
			return nil
		}
		return raw
	}
	if b, ok := v.([]byte); ok {
		if len(b) == 0 {
			return nil
		}
		return json.RawMessage(b)
	}
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return nil
	}
	return json.RawMessage(data)
}

// --- shared semantic mutation helpers ---
// Both canonical_reducer.go and canonical_transcript.go use these so
// the two code paths apply identical mutation semantics.

// findOrCreateTurn finds an existing turn or appends a new one.
// When a new turn is created, status/createdAt metadata are seeded from the
// provided arguments. Existing turns preserve their current status unless empty.
func findOrCreateTurn(state *ConversationState, turnID string, status TurnStatus, createdAt time.Time) *TurnState {
	if state == nil {
		return nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	for _, t := range state.Turns {
		if t != nil && t.TurnID == turnID {
			if t.Status == "" && status != "" {
				t.Status = status
			}
			if t.CreatedAt.IsZero() && !createdAt.IsZero() {
				t.CreatedAt = createdAt
			}
			return t
		}
	}
	turn := &TurnState{
		TurnID:    turnID,
		Status:    status,
		CreatedAt: createdAt,
	}
	state.Turns = append(state.Turns, turn)
	return turn
}

func findOrCreateTurnWithTime(state *ConversationState, event *streaming.Event) *TurnState {
	if event == nil {
		return nil
	}
	return findOrCreateTurn(state, event.TurnID, TurnStatusRunning, event.CreatedAt)
}

// findOrCreatePage finds an existing page by iteration on the turn's execution,
// or creates and appends a new one. It ensures turn.Execution is initialised.
func findOrCreatePage(turn *TurnState, pageID string, iteration int, mode string) *ExecutionPageState {
	if turn.Execution == nil {
		turn.Execution = &ExecutionState{}
	}
	for _, p := range turn.Execution.Pages {
		if p.Iteration == iteration {
			return p
		}
	}
	page := &ExecutionPageState{
		PageID:    pageID,
		Iteration: iteration,
		Mode:      mode,
	}
	turn.Execution.Pages = append(turn.Execution.Pages, page)
	turn.Execution.ActivePageIdx = len(turn.Execution.Pages) - 1
	return page
}

func ensureCurrentPage(turn *TurnState, event *streaming.Event) *ExecutionPageState {
	if turn == nil || event == nil {
		return nil
	}
	page := findOrCreatePage(turn, strings.TrimSpace(event.AssistantMessageID), event.Iteration, strings.TrimSpace(event.Mode))
	if page == nil {
		return nil
	}
	if page.PageID == "" {
		page.PageID = strings.TrimSpace(event.AssistantMessageID)
	}
	if page.AssistantMessageID == "" {
		page.AssistantMessageID = strings.TrimSpace(event.AssistantMessageID)
	}
	if page.ParentMessageID == "" {
		page.ParentMessageID = strings.TrimSpace(event.ParentMessageID)
	}
	if page.TurnID == "" {
		page.TurnID = strings.TrimSpace(event.TurnID)
	}
	if page.Mode == "" {
		page.Mode = strings.TrimSpace(event.Mode)
	}
	return page
}

// upsertModelStep finds an existing model step by ModelCallID or appends a new one.
// When modelCallID is empty, a new step is always appended (no dedup for anonymous steps).
func upsertModelStep(page *ExecutionPageState, modelCallID string) *ModelStepState {
	modelCallID = strings.TrimSpace(modelCallID)
	if modelCallID != "" {
		for _, ms := range page.ModelSteps {
			if ms.ModelCallID == modelCallID {
				return ms
			}
		}
	}
	ms := &ModelStepState{
		ModelCallID:        modelCallID,
		AssistantMessageID: modelCallID,
	}
	page.ModelSteps = append(page.ModelSteps, ms)
	return ms
}

// upsertToolStep finds an existing tool step by ToolCallID or appends a new one.
// When toolCallID is empty, a new step is always appended (no dedup for anonymous steps).
func upsertToolStep(page *ExecutionPageState, toolCallID string) *ToolStepState {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID != "" {
		for _, ts := range page.ToolSteps {
			if ts.ToolCallID == toolCallID {
				return ts
			}
		}
	}
	ts := &ToolStepState{ToolCallID: toolCallID}
	page.ToolSteps = append(page.ToolSteps, ts)
	return ts
}

// attachLinkedConversation appends linked to turn.LinkedConversations if not already present.
func attachLinkedConversation(turn *TurnState, linked *LinkedConversationState) {
	if turn == nil || linked == nil {
		return
	}
	id := strings.TrimSpace(linked.ConversationID)
	if id == "" {
		return
	}
	for _, lc := range turn.LinkedConversations {
		if lc.ConversationID == id {
			return
		}
	}
	turn.LinkedConversations = append(turn.LinkedConversations, linked)
}

// setAssistantFinal sets the final assistant message on the turn and the page.
func setAssistantFinal(turn *TurnState, page *ExecutionPageState, messageID, content string) {
	if turn.Assistant == nil {
		turn.Assistant = &AssistantState{}
	}
	turn.Assistant.Final = &AssistantMessageState{
		MessageID: strings.TrimSpace(messageID),
		Content:   content,
	}
	if page != nil {
		page.Content = content
		page.FinalResponse = true
		page.FinalAssistantMessageID = strings.TrimSpace(messageID)
	}
}

// setAssistantPreamble sets the preamble on the turn and the page.
func setAssistantPreamble(turn *TurnState, page *ExecutionPageState, messageID, content string) {
	if turn.Assistant == nil {
		turn.Assistant = &AssistantState{}
	}
	turn.Assistant.Preamble = &AssistantMessageState{
		MessageID: strings.TrimSpace(messageID),
		Content:   content,
	}
	if page != nil {
		page.Preamble = content
		page.PreambleMessageID = strings.TrimSpace(messageID)
	}
}

// setElicitationState sets the elicitation state on the turn.
func setElicitationState(turn *TurnState, elicitation *ElicitationState) {
	if turn == nil || elicitation == nil {
		return
	}
	turn.Elicitation = elicitation
}

// finalizeTurn sets a terminal status on the turn, refusing to downgrade
// from an already-terminal status.
func finalizeTurn(turn *TurnState, status TurnStatus) {
	if turn == nil {
		return
	}
	if turn.Status == TurnStatusFailed || turn.Status == TurnStatusCanceled {
		return
	}
	turn.Status = status
}

// activateFeed adds or updates a feed in state.Feeds.
func activateFeed(state *ConversationState, feed *ActiveFeedState) {
	if state == nil || feed == nil || strings.TrimSpace(feed.FeedID) == "" {
		return
	}
	for _, f := range state.Feeds {
		if f != nil && f.FeedID == feed.FeedID {
			if feed.Title != "" {
				f.Title = feed.Title
			}
			if feed.ItemCount > 0 || f.ItemCount == 0 {
				f.ItemCount = feed.ItemCount
			}
			if feed.Data != nil {
				f.Data = feed.Data
			}
			return
		}
	}
	state.Feeds = append(state.Feeds, feed)
}

// deactivateFeed removes the feed with feedID from state.Feeds.
func deactivateFeed(state *ConversationState, feedID string) {
	if state == nil || len(state.Feeds) == 0 {
		return
	}
	feedID = strings.TrimSpace(feedID)
	if feedID == "" {
		return
	}
	filtered := state.Feeds[:0]
	for _, f := range state.Feeds {
		if f == nil || f.FeedID == feedID {
			continue
		}
		filtered = append(filtered, f)
	}
	state.Feeds = filtered
}
