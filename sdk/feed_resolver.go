package sdk

import (
	"context"
	"encoding/json"
	"strings"
)

func (c *EmbeddedClient) resolveActiveFeedsFromState(ctx context.Context, state *ConversationState) []*ActiveFeedState {
	return c.resolveActiveFeedsWithVisited(ctx, state, map[string]struct{}{})
}

func (c *EmbeddedClient) resolveActiveFeedsWithVisited(ctx context.Context, state *ConversationState, visited map[string]struct{}) []*ActiveFeedState {
	if c.feeds == nil || state == nil || len(state.Turns) == 0 {
		return nil
	}
	type toolPayload struct {
		service           string
		method            string
		requestPayloadID  string
		responsePayloadID string
	}
	type feedCollector struct {
		spec             *FeedSpec
		requestPayloads  []string
		responsePayloads []string
		turnIdx          int
	}
	collectors := map[string]*feedCollector{}
	for turnIdx, turn := range state.Turns {
		if turn == nil || turn.Execution == nil {
			continue
		}
		var turnSteps []toolPayload
		for _, page := range turn.Execution.Pages {
			if page == nil {
				continue
			}
			for _, step := range page.ToolSteps {
				if step == nil {
					continue
				}
				service, method := parseToolName(step.ToolName)
				turnSteps = append(turnSteps, toolPayload{
					service:           service,
					method:            method,
					requestPayloadID:  strings.TrimSpace(step.RequestPayloadID),
					responsePayloadID: strings.TrimSpace(step.ResponsePayloadID),
				})
			}
		}
		if len(turnSteps) == 0 {
			continue
		}
		for _, spec := range c.feeds.Specs() {
			if spec == nil || strings.TrimSpace(spec.ID) == "" {
				continue
			}
			triggered := false
			for _, step := range turnSteps {
				if matchesRule(spec.Match, step.service, step.method) {
					triggered = true
					break
				}
			}
			if !triggered {
				continue
			}
			col, exists := collectors[spec.ID]
			if !exists {
				col = &feedCollector{spec: spec, turnIdx: turnIdx}
				collectors[spec.ID] = col
			}
			isAll := strings.EqualFold(strings.TrimSpace(spec.Activation.Scope), "all")
			if !isAll && col.turnIdx != turnIdx {
				col.requestPayloads = nil
				col.responsePayloads = nil
				col.turnIdx = turnIdx
			}
			serviceRule, methodRule := feedPayloadMatch(spec)
			for _, step := range turnSteps {
				if !matchesRule(FeedMatch{Service: serviceRule, Method: methodRule}, step.service, step.method) {
					continue
				}
				if payload := c.fetchPayloadContent(ctx, step.requestPayloadID); payload != "" {
					col.requestPayloads = append(col.requestPayloads, payload)
				}
				if payload := c.fetchPayloadContent(ctx, step.responsePayloadID); payload != "" {
					col.responsePayloads = append(col.responsePayloads, payload)
				}
			}
		}
	}
	var result []*ActiveFeedState
	for _, col := range collectors {
		if len(col.requestPayloads) == 0 && len(col.responsePayloads) == 0 {
			continue
		}
		rootData := buildFeedData(col.spec.ID, col.requestPayloads, col.responsePayloads)
		itemCount := 0
		if output, ok := rootData["output"].(map[string]interface{}); ok {
			raw, _ := json.Marshal(output)
			itemCount = estimateItemCount(string(raw))
		}
		if entries, ok := rootData["entries"].([]interface{}); ok && len(entries) > itemCount {
			itemCount = len(entries)
		}
		if itemCount == 0 {
			continue
		}
		result = append(result, &ActiveFeedState{
			FeedID:    col.spec.ID,
			Title:     col.spec.Title,
			ItemCount: itemCount,
			Data:      rootData,
		})
	}
	return c.mergeLinkedConversationFeeds(ctx, result, state, visited)
}

func feedPayloadMatch(spec *FeedSpec) (string, string) {
	if spec == nil {
		return "", ""
	}
	if strings.EqualFold(strings.TrimSpace(spec.Activation.Kind), "tool_call") {
		service := strings.TrimSpace(spec.Activation.Service)
		method := strings.TrimSpace(spec.Activation.Method)
		if service != "" {
			if method == "" {
				method = "*"
			}
			return service, method
		}
	}
	return spec.Match.Service, spec.Match.Method
}

func (c *EmbeddedClient) fetchPayloadContent(ctx context.Context, payloadID string) string {
	if c.conv == nil || strings.TrimSpace(payloadID) == "" {
		return ""
	}
	if p, err := c.conv.GetPayload(ctx, payloadID); err == nil && p != nil && p.InlineBody != nil {
		return strings.TrimSpace(string(*p.InlineBody))
	}
	return ""
}

func (c *EmbeddedClient) mergeLinkedConversationFeeds(ctx context.Context, feeds []*ActiveFeedState, state *ConversationState, visited map[string]struct{}) []*ActiveFeedState {
	if state == nil || len(state.Turns) == 0 {
		return feeds
	}
	index := map[string]*ActiveFeedState{}
	result := make([]*ActiveFeedState, 0, len(feeds))
	for _, feed := range feeds {
		if feed == nil || strings.TrimSpace(feed.FeedID) == "" {
			continue
		}
		index[feed.FeedID] = feed
		result = append(result, feed)
	}
	for _, turn := range state.Turns {
		if turn == nil {
			continue
		}
		for _, linked := range turn.LinkedConversations {
			if linked == nil || strings.TrimSpace(linked.ConversationID) == "" {
				continue
			}
			childID := strings.TrimSpace(linked.ConversationID)
			if _, seen := visited[childID]; seen {
				continue
			}
			visited[childID] = struct{}{}
			child, err := c.GetTranscript(ctx, &GetTranscriptInput{
				ConversationID: childID,
			})
			if err != nil || child == nil {
				continue
			}
			childFeeds := c.resolveActiveFeedsWithVisited(ctx, child, visited)
			for _, childFeed := range childFeeds {
				if childFeed == nil || strings.TrimSpace(childFeed.FeedID) == "" {
					continue
				}
				if existing, ok := index[childFeed.FeedID]; ok {
					if childFeed.ItemCount > existing.ItemCount || existing.Data == nil {
						existing.ItemCount = childFeed.ItemCount
						existing.Data = childFeed.Data
					}
					continue
				}
				copyFeed := *childFeed
				index[copyFeed.FeedID] = &copyFeed
				result = append(result, &copyFeed)
			}
		}
	}
	return result
}
