package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/agent/plan"
	hstate "github.com/viant/xdatly/handler/state"
)

func (c *EmbeddedClient) latestAssistantResponse(ctx context.Context, conversationID string) string {
	state, err := c.GetTranscript(ctx, &GetTranscriptInput{ConversationID: conversationID})
	if err != nil || state == nil {
		return ""
	}
	for i := len(state.Turns) - 1; i >= 0; i-- {
		turn := state.Turns[i]
		if turn == nil || turn.Assistant == nil {
			continue
		}
		if turn.Assistant.Final != nil {
			if text := strings.TrimSpace(turn.Assistant.Final.Content); text != "" {
				return text
			}
		}
		if turn.Assistant.Preamble != nil {
			if text := strings.TrimSpace(turn.Assistant.Preamble.Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func (c *EmbeddedClient) GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationState, error) {
	if input == nil || strings.TrimSpace(input.ConversationID) == "" {
		return nil, errors.New("conversation ID is required")
	}
	optState := &transcriptOptions{}
	for _, option := range options {
		if option != nil {
			option(optState)
		}
	}
	sinceMessageID := ""
	sinceTurnID := ""
	if since := strings.TrimSpace(input.Since); since != "" {
		sinceTurnID = since
		if msg, err := c.conv.GetMessage(ctx, since); err == nil && msg != nil && msg.TurnId != nil && strings.TrimSpace(*msg.TurnId) != "" {
			sinceMessageID = since
			sinceTurnID = strings.TrimSpace(*msg.TurnId)
		}
	}
	conv, err := c.getTranscriptConversation(ctx, input.ConversationID, sinceTurnID, input, optState)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return &ConversationState{ConversationID: input.ConversationID}, nil
	}
	turns := conv.GetTranscript()
	c.enrichTranscriptElicitations(ctx, turns)
	pruneTranscriptNoise(turns)
	if sinceMessageID != "" {
		turns = filterTranscriptSinceMessage(turns, sinceMessageID)
	}
	state := BuildCanonicalState(input.ConversationID, turns)
	if c.feeds != nil && state != nil && optState != nil && optState.includeFeeds {
		state.Feeds = c.resolveActiveFeedsFromState(context.Background(), state)
	}
	return state, nil
}

func (c *EmbeddedClient) getTranscriptConversation(ctx context.Context, conversationID, sinceTurnID string, input *GetTranscriptInput, optsState *transcriptOptions) (*conversation.Conversation, error) {
	selectors := map[string]*QuerySelector(nil)
	if optsState != nil {
		selectors = optsState.selectors
	}
	includeModelCalls := true
	includeToolCalls := true
	if c.data != nil && len(selectors) > 0 {
		in := &agconv.ConversationInput{
			Id:                conversationID,
			IncludeTranscript: true,
			IncludeModelCal:   includeModelCalls,
			IncludeToolCall:   includeToolCalls,
			Has: &agconv.ConversationInputHas{
				Id:                true,
				IncludeTranscript: true,
				IncludeModelCal:   true,
				IncludeToolCall:   true,
			},
		}
		if strings.TrimSpace(sinceTurnID) != "" {
			in.Since = sinceTurnID
			in.Has.Since = true
		}
		dataOpts := make([]data.Option, 0, len(selectors))
		if namedSelectors := buildTranscriptQuerySelectors(selectors); len(namedSelectors) > 0 {
			dataOpts = append(dataOpts, data.WithQuerySelector(namedSelectors...))
		}
		got, err := c.data.GetConversation(ctx, conversationID, in, dataOpts...)
		if err != nil {
			return nil, err
		}
		if got == nil {
			return nil, nil
		}
		return (*conversation.Conversation)(got), nil
	}
	var opts []conversation.Option
	opts = append(opts, conversation.WithIncludeModelCall(includeModelCalls), conversation.WithIncludeToolCall(includeToolCalls))
	if strings.TrimSpace(sinceTurnID) != "" {
		opts = append(opts, conversation.WithSince(sinceTurnID))
	}
	return c.conv.GetConversation(ctx, conversationID, opts...)
}

func buildTranscriptQuerySelectors(selectors map[string]*QuerySelector) []*hstate.NamedQuerySelector {
	if len(selectors) == 0 {
		return nil
	}
	names := []string{TranscriptSelectorTurn, TranscriptSelectorMessage, TranscriptSelectorToolMessage}
	result := make([]*hstate.NamedQuerySelector, 0, len(selectors))
	for _, name := range names {
		selector := selectors[name]
		if selector == nil {
			continue
		}
		result = append(result, &hstate.NamedQuerySelector{
			Name: name,
			QuerySelector: hstate.QuerySelector{
				Limit: selector.Limit, Offset: selector.Offset, OrderBy: selector.OrderBy,
			},
		})
	}
	return result
}

func (c *EmbeddedClient) enrichTranscriptElicitations(ctx context.Context, turns conversation.Transcript) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if elicitation := c.resolveMessageElicitation(ctx, msg); len(elicitation) > 0 {
				if content, ok := elicitation["message"].(string); ok {
					content = strings.TrimSpace(content)
					if content != "" && shouldNormalizeElicitationContent(valueOrEmpty(msg.Content)) {
						msg.Content = &content
					}
				}
			}
		}
	}
}

func shouldNormalizeElicitationContent(content string) bool {
	content = strings.TrimSpace(content)
	return content == "" || strings.HasPrefix(content, "{") || strings.HasPrefix(content, "map[")
}

func pruneTranscriptNoise(turns conversation.Transcript) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		filtered := turn.Message[:0]
		for _, msg := range turn.Message {
			if shouldDropTranscriptMessage(msg) {
				continue
			}
			filtered = append(filtered, msg)
		}
		turn.Message = filtered
	}
}

func shouldDropTranscriptMessage(msg *agconv.MessageView) bool {
	if msg == nil {
		return true
	}
	if msg.Interim != 1 || !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
		return false
	}
	if strings.TrimSpace(valueOrEmpty(msg.Content)) != "" || strings.TrimSpace(valueOrEmpty(msg.RawContent)) != "" || strings.TrimSpace(valueOrEmpty(msg.Preamble)) != "" {
		return false
	}
	if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
		return false
	}
	return !(msg.ModelCall != nil || len(msg.ToolMessage) > 0)
}

func (c *EmbeddedClient) resolveMessageElicitation(ctx context.Context, msg *agconv.MessageView) map[string]interface{} {
	if msg == nil || msg.ElicitationId == nil || strings.TrimSpace(*msg.ElicitationId) == "" {
		return nil
	}
	if msg.Elicitation != nil {
		return msg.Elicitation
	}
	return c.resolveElicitationPayload(ctx, strings.TrimSpace(*msg.ElicitationId), valueOrEmpty(msg.ElicitationPayloadId), valueOrEmpty(msg.Content))
}

func (c *EmbeddedClient) resolveElicitationPayload(ctx context.Context, elicitationID, payloadID, content string) map[string]interface{} {
	elicitationID = strings.TrimSpace(elicitationID)
	if elicitationID == "" {
		return nil
	}
	payloadID = strings.TrimSpace(payloadID)
	content = strings.TrimSpace(content)
	if payloadID != "" {
		if payload, err := c.GetPayload(ctx, payloadID); err == nil && payload != nil && payload.InlineBody != nil && len(*payload.InlineBody) > 0 {
			var elicitation map[string]interface{}
			if err = json.Unmarshal(*payload.InlineBody, &elicitation); err == nil {
				elicitation["elicitationId"] = elicitationID
				if content != "" {
					if _, ok := elicitation["message"]; !ok {
						elicitation["message"] = content
					}
				}
				return elicitation
			}
		}
	}
	if content != "" {
		var elicitation plan.Elicitation
		if err := json.Unmarshal([]byte(content), &elicitation); err == nil && !elicitation.IsEmpty() {
			raw, err := json.Marshal(elicitation)
			if err == nil {
				out := map[string]interface{}{}
				if err = json.Unmarshal(raw, &out); err == nil {
					out["elicitationId"] = elicitationID
					return out
				}
			}
		}
	}
	return nil
}

func filterTranscriptSinceMessage(turns conversation.Transcript, messageID string) conversation.Transcript {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return turns
	}
	result := make(conversation.Transcript, 0, len(turns))
	found := false
	for _, turn := range turns {
		if turn == nil {
			continue
		}
		if !found {
			index := -1
			for i, msg := range turn.Message {
				if msg != nil && strings.TrimSpace(msg.Id) == messageID {
					index = i
					break
				}
			}
			if index == -1 {
				continue
			}
			found = true
			cloned := *turn
			cloned.Message = append([]*agconv.MessageView(nil), turn.Message[index:]...)
			result = append(result, &cloned)
			continue
		}
		result = append(result, turn)
	}
	if found {
		return result
	}
	return turns
}
