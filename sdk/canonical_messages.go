package sdk

import "github.com/viant/agently-core/runtime/streaming"

// SelectStandaloneAssistantMessages returns public assistant messages that are
// independent of execution pages, narration and the final response. It uses the
// same ownership rules as native AGUI history and preserves canonical order.
// Repeated message IDs are retained; selection does not perform deduplication.
// Callers decide when a turn is ready for publication; running turns may also
// contain public messages. Returned messages are read-only canonical state.
func SelectStandaloneAssistantMessages(turn *TurnState) []*TurnMessageState {
	internalIDs, toolResultIDs := canonicalMessageExclusions([]*TurnState{turn})
	return selectStandaloneAssistantMessages(turn, internalIDs, toolResultIDs)
}

func canonicalMessageExclusions(turns []*TurnState) (map[string]bool, map[string]bool) {
	internalIDs := map[string]bool{}
	toolResultIDs := map[string]bool{}
	for _, turn := range turns {
		if turn == nil {
			continue
		}
		for _, message := range turn.Messages {
			if message != nil && message.Role == "assistant" && streaming.IsInternalMessageMode(message.Mode) {
				internalIDs[message.MessageID] = true
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page == nil {
					continue
				}
				for _, step := range page.ToolSteps {
					if step != nil && step.ToolMessageID != "" {
						toolResultIDs[step.ToolMessageID] = true
					}
				}
				hidden := streaming.IsInternalMessageMode(page.Mode)
				if hidden && page.AssistantMessageID != "" {
					internalIDs[page.AssistantMessageID] = true
				}
				for _, step := range page.ModelSteps {
					if step != nil && (streaming.IsInternalMessageMode(step.Mode) || step.Mode == "" && (hidden || streaming.IsInternalMessageMode(step.ExecutionRole))) && step.AssistantMessageID != "" {
						internalIDs[step.AssistantMessageID] = true
					}
				}
			}
		}
	}
	return internalIDs, toolResultIDs
}

func selectStandaloneAssistantMessages(turn *TurnState, internalIDs, toolResultIDs map[string]bool) []*TurnMessageState {
	if turn == nil {
		return nil
	}
	var result []*TurnMessageState
	pageOwned := map[string]bool{}
	if turn.Assistant != nil {
		if turn.Assistant.Final != nil {
			pageOwned[turn.Assistant.Final.MessageID] = true
		}
		if turn.Assistant.Narration != nil {
			pageOwned[turn.Assistant.Narration.MessageID] = true
		}
	}
	if turn.Execution != nil {
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, id := range []string{page.AssistantMessageID, page.NarrationMessageID, page.FinalAssistantMessageID} {
				if id != "" {
					pageOwned[id] = true
				}
			}
			for _, step := range page.ModelSteps {
				if step != nil && step.AssistantMessageID != "" {
					pageOwned[step.AssistantMessageID] = true
				}
			}
		}
	}
	for _, message := range turn.Messages {
		if message == nil || message.Role != "assistant" || message.MessageID == "" || message.Interim > 0 || pageOwned[message.MessageID] || internalIDs[message.MessageID] || toolResultIDs[message.MessageID] {
			continue
		}
		result = append(result, message)
	}
	return result
}
