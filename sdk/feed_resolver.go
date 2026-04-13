package sdk

import (
	"context"
	"strings"
)

func (c *backendClient) resolveActiveFeedsFromState(ctx context.Context, state *ConversationState) []*ActiveFeedState {
	return c.resolveActiveFeedsWithVisited(ctx, state, map[string]struct{}{})
}

func (c *backendClient) resolveActiveFeedsWithVisited(ctx context.Context, state *ConversationState, visited map[string]struct{}) []*ActiveFeedState {
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

	// Phase 1: collect matching tool steps and their payload IDs (no fetching yet).
	var allPayloadIDs []string
	payloadIDSet := map[string]struct{}{}
	addPayloadID := func(id string) {
		if id = strings.TrimSpace(id); id != "" {
			if _, seen := payloadIDSet[id]; !seen {
				payloadIDSet[id] = struct{}{}
				allPayloadIDs = append(allPayloadIDs, id)
			}
		}
	}

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
				addPayloadID(step.requestPayloadID)
				addPayloadID(step.responsePayloadID)
			}
		}
	}

	// Phase 2: batch-fetch all needed payloads.
	payloadContents := map[string]string{}
	if len(allPayloadIDs) > 0 {
		fetched, err := c.GetPayloads(ctx, allPayloadIDs)
		if err == nil {
			for id, p := range fetched {
				if p != nil && p.InlineBody != nil {
					if body := strings.TrimSpace(string(*p.InlineBody)); body != "" {
						payloadContents[id] = body
					}
				}
			}
		}
	}

	// Phase 3: populate collectors with fetched payload content.
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
			col, exists := collectors[spec.ID]
			if !exists {
				continue
			}
			isAll := strings.EqualFold(strings.TrimSpace(spec.Activation.Scope), "all")
			if !isAll && col.turnIdx != turnIdx {
				continue
			}
			serviceRule, methodRule := feedPayloadMatch(spec)
			for _, step := range turnSteps {
				if !matchesRule(FeedMatch{Service: serviceRule, Method: methodRule}, step.service, step.method) {
					continue
				}
				if body := payloadContents[step.requestPayloadID]; body != "" {
					col.requestPayloads = append(col.requestPayloads, body)
				}
				if body := payloadContents[step.responsePayloadID]; body != "" {
					col.responsePayloads = append(col.responsePayloads, body)
				}
			}
		}
	}

	var result []*ActiveFeedState
	for _, col := range collectors {
		if len(col.requestPayloads) == 0 && len(col.responsePayloads) == 0 {
			continue
		}
		resultSet, err := extractFeedData(col.spec, col.requestPayloads, col.responsePayloads)
		if err != nil || resultSet == nil || resultSet.RootData == nil {
			continue
		}
		rootData := resultSet.RootData
		itemCount := resultSet.ItemCount
		if itemCount == 0 {
			continue
		}
		result = append(result, &ActiveFeedState{
			FeedID:    col.spec.ID,
			Title:     col.spec.Title,
			ItemCount: itemCount,
			Data:      marshalToRawJSON(rootData),
		})
	}
	return result
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

func (c *backendClient) fetchPayloadContent(ctx context.Context, payloadID string) string {
	if c.conv == nil || strings.TrimSpace(payloadID) == "" {
		return ""
	}
	if p, err := c.conv.GetPayload(ctx, payloadID); err == nil && p != nil && p.InlineBody != nil {
		return strings.TrimSpace(string(*p.InlineBody))
	}
	return ""
}

// resolveLinkedChildStates fetches canonical state for all unique linked child
// conversations referenced in state.Turns. Each child is fetched at most once
// (visited tracking prevents cycles). Returns a map of conversationID → ConversationState.
func (c *backendClient) resolveLinkedChildStates(ctx context.Context, state *ConversationState, visited map[string]struct{}) map[string]*ConversationState {
	result := map[string]*ConversationState{}
	if state == nil {
		return result
	}
	// Collect all unique child IDs first to allow future batching.
	var childIDs []string
	for _, turn := range state.Turns {
		if turn == nil {
			continue
		}
		for _, linked := range turn.LinkedConversations {
			if linked == nil {
				continue
			}
			childID := strings.TrimSpace(linked.ConversationID)
			if childID == "" {
				continue
			}
			if _, seen := visited[childID]; seen {
				continue
			}
			visited[childID] = struct{}{}
			childIDs = append(childIDs, childID)
		}
	}
	// Fetch each child transcript. This loop is the remaining N+1 candidate;
	// a BatchGetTranscript API would collapse it to a single call when available.
	for _, childID := range childIDs {
		resp, err := c.GetTranscript(ctx, &GetTranscriptInput{ConversationID: childID})
		if err != nil || resp == nil || resp.Conversation == nil {
			continue
		}
		result[childID] = resp.Conversation
	}
	return result
}

// mergeLinkedConversationFeeds resolves feeds from linked child conversations and
// merges them into the parent feed list. It batch-fetches all child states before
// resolving feeds so feed resolution itself does no additional transcript fetching.
func (c *backendClient) mergeLinkedConversationFeeds(ctx context.Context, feeds []*ActiveFeedState, state *ConversationState, visited map[string]struct{}) []*ActiveFeedState {
	if state == nil || len(state.Turns) == 0 {
		return feeds
	}

	// Build working index from parent feeds.
	index := map[string]*ActiveFeedState{}
	result := make([]*ActiveFeedState, 0, len(feeds))
	for _, feed := range feeds {
		if feed == nil || strings.TrimSpace(feed.FeedID) == "" {
			continue
		}
		index[feed.FeedID] = feed
		result = append(result, feed)
	}

	// Batch-fetch child states, then resolve their feeds.
	childStates := c.resolveLinkedChildStates(ctx, state, visited)
	for _, childState := range childStates {
		childFeeds := c.resolveActiveFeedsWithVisited(ctx, childState, visited)
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
	return result
}
