package sdk

import (
	"encoding/json"
	"github.com/viant/agently-core/runtime/aguistate"
)

// Canonical host turns remain in the exact authorized transcript DTO. They do
// not become model-facing standard messages or ordinary chat presentation.
func aguiCanonicalHostBoundaries(transcript *ConversationStateResponse) (map[string]bool, map[string]bool) {
	turns, ids := map[string]bool{}, map[string]bool{}
	add := func(id string) {
		if id != "" {
			ids[id] = true
		}
	}
	if transcript == nil || transcript.Conversation == nil {
		return turns, ids
	}
	for _, turn := range transcript.Conversation.Turns {
		if turn == nil || turn.Origin != "host_request" {
			continue
		}
		turns[turn.TurnID] = true
		add(turn.StartedByMessageID)
		if turn.User != nil {
			add(turn.User.MessageID)
		}
		for _, message := range turn.Users {
			if message != nil {
				add(message.MessageID)
			}
		}
		for _, message := range turn.Messages {
			if message != nil {
				add(message.MessageID)
			}
		}
		if turn.Assistant != nil {
			for _, message := range append(append([]*AssistantMessageState{}, turn.Assistant.Messages...), turn.Assistant.Narration, turn.Assistant.Final) {
				if message != nil {
					add(message.MessageID)
				}
			}
		}
		if turn.Execution != nil {
			for _, page := range turn.Execution.Pages {
				if page == nil {
					continue
				}
				for _, step := range page.ToolSteps {
					if step != nil {
						add(step.ToolMessageID)
					}
				}
				for _, step := range page.ModelSteps {
					if step != nil {
						add(step.AssistantMessageID)
					}
				}
			}
		}
	}
	return turns, ids
}
func aguiMessageNativeTurn(message aguistate.Object) string {
	var metadata struct {
		Agently struct {
			Presentation struct {
				NativeTurnID string `json:"nativeTurnId"`
			} `json:"presentation"`
		} `json:"agently"`
	}
	_ = json.Unmarshal(rawAGUI(message["metadata"]), &metadata)
	if metadata.Agently.Presentation.NativeTurnID != "" {
		return metadata.Agently.Presentation.NativeTurnID
	}
	if message["role"] == "activity" {
		kind, _ := message["activityType"].(string)
		switch kind {
		case "agently.turn", "agently.tool", "agently.user-identity":
			var content struct {
				NativeTurnID string `json:"nativeTurnId"`
			}
			_ = json.Unmarshal(rawAGUI(message["content"]), &content)
			return content.NativeTurnID
		}
	}
	return ""
}
