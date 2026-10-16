package agent

import (
	"context"
	"encoding/json"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/protocol/mcpname"
	elicaction "github.com/viant/agently-core/service/elicitation/action"
)

// completedApprovalReviews projects a resolved execution review into its tool
// result. An approval callback is not a new conversational task after that
// execution. Native transcript rows, original arguments and AG-UI remain intact.
// Missing or ambiguous associations retain the original history unchanged.
func (s *Service) completedApprovalReviews(ctx context.Context, transcript apiconv.Transcript, hiddenIDs ...string) (map[string]json.RawMessage, []string) {
	reviews := map[string]json.RawMessage{}
	var responses []string
	hidden := make(map[string]bool, len(hiddenIDs))
	for _, id := range hiddenIDs {
		hidden[id] = true
	}
	if s == nil || s.conversation == nil {
		return reviews, responses
	}
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		messages := turn.GetMessages()
		for _, request := range messages {
			if request == nil || strings.TrimSpace(request.Id) == "" || hidden[request.Id] || request.IsArchived() || request.Role != "assistant" || request.ElicitationId == nil || strings.TrimSpace(*request.ElicitationId) == "" || request.ElicitationPayloadId == nil || request.Status == nil || *request.Status != elicaction.StatusAccepted {
				continue
			}
			stored, err := s.conversation.GetPayload(ctx, *request.ElicitationPayloadId)
			if err != nil || stored == nil || stored.Kind != "elicitation_request" || stored.InlineBody == nil {
				continue
			}
			var elic execution.Elicitation
			if json.Unmarshal(*stored.InlineBody, &elic) != nil {
				continue
			}
			kind, _ := elic.RequestedSchema.Properties["_type"].(map[string]interface{})
			name, _ := elic.RequestedSchema.Properties["_toolName"].(map[string]interface{})
			toolName, _ := name["const"].(string)
			if kind["const"] != "tool_approval" || strings.TrimSpace(toolName) == "" {
				continue
			}
			// Record stores dedicated approval UI text on the model message. Never
			// hide a parent that now carries authored assistant narration instead.
			if strings.TrimSpace(elic.Message) == "" || request.Content == nil || strings.TrimSpace(*request.Content) != strings.TrimSpace(elic.Message) {
				continue
			}
			if request.RawContent != nil && strings.TrimSpace(*request.RawContent) != "" && strings.TrimSpace(*request.RawContent) != strings.TrimSpace(elic.Message) {
				continue
			}
			var toolID string
			var toolMessage *apiconv.Message
			matches := 0
			for _, candidate := range messages {
				if candidate == nil || candidate.ParentMessageId == nil || *candidate.ParentMessageId != request.Id || candidate.ConversationId != request.ConversationId || !isConcreteToolResultMessage(candidate) {
					continue
				}
				call := messageToolCall(candidate)
				if call == nil || mcpname.Canonical(call.ToolName) != mcpname.Canonical(toolName) {
					continue
				}
				matches++
				if call.Status == "completed" && strings.TrimSpace(call.OpId) != "" {
					toolID = candidate.Id
					toolMessage = candidate
				}
			}
			if matches != 1 || toolID == "" {
				continue
			}
			if hidden[toolID] || toolMessage.IsArchived() {
				continue
			}
			if strings.TrimSpace(s.messageToolResultBody(ctx, toolMessage)) == "" {
				continue
			}
			var answer *apiconv.Message
			for _, candidate := range messages {
				if candidate == nil || candidate.Role != "user" || candidate.Type != "elicitation_response" || candidate.ParentMessageId == nil || *candidate.ParentMessageId != request.Id || candidate.ElicitationId == nil || *candidate.ElicitationId != *request.ElicitationId || candidate.ConversationId != request.ConversationId {
					continue
				}
				if answer != nil {
					answer = nil
					break
				}
				answer = candidate
			}
			if answer == nil || hidden[answer.Id] || answer.IsArchived() || !json.Valid([]byte(answer.GetContent())) {
				continue
			}
			review, err := json.Marshal(struct {
				Action  string          `json:"action"`
				Payload json.RawMessage `json:"payload"`
			}{elicaction.Accept, json.RawMessage(answer.GetContent())})
			if err != nil {
				continue
			}
			reviews[toolID] = review
			responses = append(responses, request.Id, answer.Id)
		}
	}
	return reviews, responses
}

func projectToolApprovalReview(body string, review json.RawMessage) string {
	if len(review) == 0 {
		return body
	}
	// This trailer belongs only to model history. The tool's exact output remains
	// the prefix; the review is explicitly distinguished from its business data.
	return body + "\n\nExecution review for this completed tool call (persisted accepted user decision; review metadata):\n" + string(review)
}
