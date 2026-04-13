package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/protocol/agent/plan"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

func (c *backendClient) FeedRegistry() *FeedRegistry {
	return c.feeds
}

func (c *backendClient) ListFeedSpecs() []*FeedSpec {
	if c.feeds == nil {
		return nil
	}
	return c.feeds.Specs()
}

func (c *backendClient) ResolveFeedData(ctx context.Context, spec *FeedSpec, conversationID string) (interface{}, error) {
	if spec == nil || conversationID == "" || c.conv == nil {
		return nil, nil
	}
	conv, err := c.conv.GetConversation(ctx, conversationID, conversation.WithIncludeTranscript(true), conversation.WithIncludeToolCall(true))
	if err != nil || conv == nil {
		return nil, err
	}
	useLast := strings.ToLower(strings.TrimSpace(spec.Activation.Scope)) != "all"
	transcript := conv.GetTranscript()
	for i := len(transcript) - 1; i >= 0; i-- {
		turn := transcript[i]
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || msg.ToolName == nil {
				continue
			}
			toolSvc, toolMtd := parseToolName(*msg.ToolName)
			if !matchesRule(spec.Match, toolSvc, toolMtd) {
				continue
			}
			content := ""
			if msg.Content != nil {
				content = strings.TrimSpace(*msg.Content)
			}
			if content == "" {
				continue
			}
			var parsed interface{}
			if err := json.Unmarshal([]byte(content), &parsed); err == nil && useLast {
				return map[string]interface{}{"output": parsed}, nil
			}
		}
	}
	return nil, nil
}

func (c *backendClient) RecordOOBAuthElicitation(ctx context.Context, authURL string) error {
	if c.elicSvc == nil {
		return fmt.Errorf("elicitation service not configured")
	}
	convID := runtimerequestctx.ConversationIDFromContext(ctx)
	turnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = turn.TurnID
		if convID == "" {
			convID = turn.ConversationID
		}
	}
	if convID == "" {
		return fmt.Errorf("no conversation in context for OOB auth")
	}
	turn := runtimerequestctx.TurnMeta{ConversationID: convID, TurnID: turnID}
	elic := &plan.Elicitation{}
	elic.Message = "MCP server requires authentication. Please sign in to continue."
	elic.Mode = "url"
	elic.Url = authURL
	_, err := c.elicSvc.Record(ctx, &turn, "assistant", elic)
	return err
}

// Deprecated: use resolveActiveFeedsFromState instead.
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
				FeedID:    spec.ID,
				Title:     spec.Title,
				ItemCount: itemCount,
				Data:      marshalToRawJSON(data),
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
			return strings.TrimSpace(string(*p.InlineBody))
		}
	}
	return ""
}

func (c *backendClient) GetPayload(ctx context.Context, id string) (*conversation.Payload, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	payloadID := strings.TrimSpace(id)
	if payloadID == "" {
		return nil, errors.New("payload ID is required")
	}
	return c.conv.GetPayload(ctx, payloadID)
}

// GetPayloads fetches multiple payloads by ID in one call.
// IDs that are empty or not found are silently omitted from the result.
func (c *backendClient) GetPayloads(ctx context.Context, ids []string) (map[string]*conversation.Payload, error) {
	if c.conv == nil {
		return nil, errors.New("conversation client not configured")
	}
	result := make(map[string]*conversation.Payload, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, already := result[id]; already {
			continue
		}
		p, err := c.conv.GetPayload(ctx, id)
		if err != nil {
			log.Printf("[sdk] GetPayloads: failed to fetch payload %q: %v", id, err)
			continue
		}
		if p == nil {
			continue
		}
		result[id] = p
	}
	return result, nil
}
