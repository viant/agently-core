package sdk

import (
	"context"
	"encoding/json"
	"github.com/viant/agently-core/app/store/conversation"
	"strings"
)

// Historical feed behavior reference retained only for comparison tests.
func (c *backendClient) resolveActiveFeeds(ctx context.Context, turns conversation.Transcript) []*ActiveFeedState {
	if c.feeds == nil || len(turns) == 0 {
		return nil
	}
	toolNames := turns.UniqueToolNames()
	feedResults := map[string]*ActiveFeedState{}
	for _, toolName := range toolNames {
		matched := c.feeds.Match(toolName)
		for _, spec := range matched {
			if _, exists := feedResults[spec.ID]; exists {
				continue
			}
			content := c.findLastToolCallPayload(ctx, turns, toolName)
			var data interface{}
			if content != "" {
				var parsed interface{}
				if err := json.Unmarshal([]byte(content), &parsed); err == nil {
					data = map[string]interface{}{"output": parsed}
				}
			}
			itemCount := 0
			if content != "" {
				itemCount = estimateItemCount(content)
			}
			feedResults[spec.ID] = &ActiveFeedState{
				FeedID:        spec.ID,
				Title:         spec.Title,
				DeveloperOnly: spec.DeveloperOnly,
				Presentation:  normalizedFeedPresentation(spec),
				ItemCount:     itemCount,
				Data:          marshalToRawJSON(data),
			}
		}
	}
	if len(feedResults) == 0 {
		return nil
	}
	result := make([]*ActiveFeedState, 0, len(feedResults))
	for _, f := range feedResults {
		if f.ItemCount > 0 || f.Data != nil {
			result = append(result, f)
		}
	}
	return result
}

func (c *backendClient) findLastToolCallPayload(ctx context.Context, turns conversation.Transcript, targetTool string) string {
	target := strings.ToLower(strings.TrimSpace(targetTool))
	for i := len(turns) - 1; i >= 0; i-- {
		turn := turns[i]
		if turn == nil {
			continue
		}
		for j := len(turn.Message) - 1; j >= 0; j-- {
			msg := turn.Message[j]
			if msg == nil || msg.ToolName == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(*msg.ToolName)) != target {
				continue
			}
			if msg.Content != nil && strings.TrimSpace(*msg.Content) != "" {
				return strings.TrimSpace(*msg.Content)
			}
			if c.conv != nil {
				if payloadContent := c.fetchToolCallResponsePayload(ctx, msg.Id); payloadContent != "" {
					return payloadContent
				}
			}
		}
	}
	return ""
}

func (c *backendClient) fetchToolCallResponsePayload(ctx context.Context, messageID string) string {
	if c.conv == nil || messageID == "" {
		return ""
	}
	msg, err := c.conv.GetMessage(ctx, messageID)
	if err != nil || msg == nil {
		return ""
	}
	for _, tm := range msg.ToolMessage {
		if tm == nil || tm.ToolCall == nil || tm.ToolCall.ResponsePayloadId == nil {
			continue
		}
		payloadID := strings.TrimSpace(*tm.ToolCall.ResponsePayloadId)
		if payloadID == "" {
			continue
		}
		if p, err := c.conv.GetPayload(ctx, payloadID); err == nil && p != nil && p.InlineBody != nil {
			return strings.TrimSpace(conversation.DecodeInlineBody(string(*p.InlineBody), p.Compression))
		}
	}
	return ""
}
