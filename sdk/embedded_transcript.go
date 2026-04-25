package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/agent/plan"
	hstate "github.com/viant/xdatly/handler/state"
)

func (c *backendClient) latestAssistantResponse(ctx context.Context, conversationID string) string {
	resp, err := c.GetTranscript(ctx, &GetTranscriptInput{ConversationID: conversationID})
	if err != nil || resp == nil || resp.Conversation == nil {
		return ""
	}
	for i := len(resp.Conversation.Turns) - 1; i >= 0; i-- {
		turn := resp.Conversation.Turns[i]
		if turn == nil || turn.Assistant == nil {
			continue
		}
		if turn.Assistant.Final != nil {
			if text := strings.TrimSpace(turn.Assistant.Final.Content); text != "" {
				return text
			}
		}
		if turn.Assistant.Narration != nil {
			if text := strings.TrimSpace(turn.Assistant.Narration.Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func (c *backendClient) GetTranscript(ctx context.Context, input *GetTranscriptInput, options ...TranscriptOption) (*ConversationStateResponse, error) {
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
		return &ConversationStateResponse{
			SchemaVersion: "2",
			Conversation:  &ConversationState{ConversationID: input.ConversationID},
		}, nil
	}
	turns := conv.GetTranscript()
	c.enrichTranscriptElicitations(ctx, turns)
	pruneTranscriptNoise(turns)
	if sinceMessageID != "" {
		turns = filterTranscriptSinceMessage(turns, sinceMessageID)
	}
	state := BuildCanonicalState(input.ConversationID, turns)
	resp := &ConversationStateResponse{
		SchemaVersion: "2",
		Conversation:  state,
		Usage:         usageSummaryFromConversation(conv),
	}
	if c.feeds != nil && state != nil && optState != nil && optState.includeFeeds {
		resp.Feeds = c.resolveActiveFeedsFromState(ctx, state)
	}
	return resp, nil
}

func usageSummaryFromConversation(conv *conversation.Conversation) *UsageSummary {
	if conv == nil {
		return nil
	}
	input := 0
	output := 0
	if conv.UsageInputTokens != nil {
		input = *conv.UsageInputTokens
	}
	if conv.UsageOutputTokens != nil {
		output = *conv.UsageOutputTokens
	}
	if input == 0 && output == 0 && conv.Usage != nil {
		if conv.Usage.PromptTokens != nil {
			input = *conv.Usage.PromptTokens
		}
		if conv.Usage.CompletionTokens != nil {
			output = *conv.Usage.CompletionTokens
		}
	}
	if input == 0 && output == 0 {
		return nil
	}
	return &UsageSummary{
		TotalInputTokens:  input,
		TotalOutputTokens: output,
	}
}

// GetLiveState returns the current canonical state snapshot together with an
// EventCursor for SSE reconnection. On connect or reconnect the caller should:
//  1. Call GetLiveState to get the snapshot and cursor.
//  2. Begin consuming the event stream.
//  3. Discard events whose CreatedAt is before the cursor timestamp.
//  4. Apply subsequent events via Reduce() against the snapshot.
//
// The cursor is an RFC3339Nano timestamp captured just before the snapshot fetch,
// so any event published concurrently will be at or after the cursor.
func (c *backendClient) GetLiveState(ctx context.Context, conversationID string, options ...TranscriptOption) (*ConversationStateResponse, error) {
	cursor := time.Now().UTC().Format(time.RFC3339Nano)
	resp, err := c.GetTranscript(ctx, &GetTranscriptInput{ConversationID: conversationID}, options...)
	if err != nil {
		return nil, err
	}
	resp.EventCursor = cursor
	return resp, nil
}

func (c *backendClient) getTranscriptConversation(ctx context.Context, conversationID, sinceTurnID string, input *GetTranscriptInput, optsState *transcriptOptions) (*conversation.Conversation, error) {
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

func (c *backendClient) enrichTranscriptElicitations(ctx context.Context, turns conversation.Transcript) {
	for _, turn := range turns {
		if turn == nil || len(turn.Message) == 0 {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if elicitation := c.resolveMessageElicitation(ctx, msg); len(elicitation) > 0 {
				// Populate the full elicitation map so buildElicitationState
				// can read requestedSchema and callbackUrl from it directly,
				// without parsing embedded content JSON.
				if msg.Elicitation == nil {
					msg.Elicitation = elicitation
				}
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
	if valueOrEmpty(msg.Content) != "" || valueOrEmpty(msg.RawContent) != "" || valueOrEmpty(msg.Narration) != "" {
		return false
	}
	if msg.ElicitationId != nil && strings.TrimSpace(*msg.ElicitationId) != "" {
		return false
	}
	return !(msg.ModelCall != nil || len(msg.ToolMessage) > 0)
}

func (c *backendClient) resolveMessageElicitation(ctx context.Context, msg *agconv.MessageView) map[string]interface{} {
	if msg == nil || msg.ElicitationId == nil || strings.TrimSpace(*msg.ElicitationId) == "" {
		return nil
	}
	if msg.Elicitation != nil {
		return msg.Elicitation
	}
	return c.resolveElicitationPayload(ctx, strings.TrimSpace(*msg.ElicitationId), valueOrEmpty(msg.ElicitationPayloadId), valueOrEmpty(msg.Content))
}

func (c *backendClient) resolveElicitationPayload(ctx context.Context, elicitationID, payloadID, content string) map[string]interface{} {
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
