package sdk

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
)

// feedNotifier implements executil.FeedNotifier. It checks completed tool
// calls against the feed registry and emits tool_feed_active/inactive SSE
// events via the streaming bus.
type feedNotifier struct {
	registry *FeedRegistry
	bus      streaming.Bus

	mu          sync.Mutex
	activeFeeds map[string]map[string]bool // conversationID -> feedID -> active in current turn
}

func newFeedNotifier(registry *FeedRegistry, bus streaming.Bus) *feedNotifier {
	return &feedNotifier{
		registry:    registry,
		bus:         bus,
		activeFeeds: map[string]map[string]bool{},
	}
}

// NotifyToolCompleted checks if the completed tool matches any feed spec
// and emits a tool_feed_active SSE event.
func (n *feedNotifier) NotifyToolCompleted(ctx context.Context, toolName string, result string) {
	if n.registry == nil || n.bus == nil {
		return
	}
	matched := n.registry.Match(toolName)
	if len(matched) > 0 {
		log.Printf("[feed-notifier] tool=%q matched %d feeds", toolName, len(matched))
	}
	if len(matched) == 0 {
		return
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
		return
	}

	// Collect all map writes under one lock acquisition to avoid interleaving
	// with concurrent EmitInactiveForMissing calls between loop iterations.
	n.mu.Lock()
	feedsByConversation := n.activeFeeds[convID]
	if feedsByConversation == nil {
		feedsByConversation = map[string]bool{}
		n.activeFeeds[convID] = feedsByConversation
	}
	for _, spec := range matched {
		feedsByConversation[spec.ID] = true
	}
	n.mu.Unlock()
	// Emit SSE events outside the lock — bus.Publish must not be called under
	// n.mu to avoid deadlock if the bus callback re-enters the notifier.
	for _, spec := range matched {
		feedData, feedCount := genericNotifierFeedPayload(spec, result)
		itemCount := feedCount
		if itemCount == 0 && feedData != nil {
			itemCount = estimateItemCount(result)
		}
		if feedData == nil && itemCount == 0 {
			continue
		}
		emitFeedActive(ctx, n.bus, convID, turnID, spec, itemCount, feedData)
	}
}

// EmitInactiveForMissing emits tool_feed_inactive for feeds that were
// previously active but are no longer matched in the current turn.
func (n *feedNotifier) EmitInactiveForMissing(ctx context.Context, convID string, currentToolNames []string) {
	if n.registry == nil || n.bus == nil {
		return
	}
	// Build set of currently matched feed IDs.
	currentFeedIDs := map[string]bool{}
	for _, name := range currentToolNames {
		for _, spec := range n.registry.Match(name) {
			currentFeedIDs[spec.ID] = true
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	feedsByConversation := n.activeFeeds[convID]
	for feedID := range feedsByConversation {
		if !currentFeedIDs[feedID] {
			emitFeedInactive(ctx, n.bus, convID, feedID)
			delete(feedsByConversation, feedID)
		}
	}
	if len(feedsByConversation) == 0 {
		delete(n.activeFeeds, convID)
	}
}

// estimateItemCount tries to derive a count from the tool result.
func estimateItemCount(result string) int {
	result = strings.TrimSpace(result)
	if result == "" {
		return 0
	}
	// Try JSON array.
	var arr []interface{}
	if err := json.Unmarshal([]byte(result), &arr); err == nil {
		return len(arr)
	}
	// Try JSON object with known list fields.
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(result), &obj); err == nil {
		for _, key := range []string{"plan", "steps", "commands", "files", "changes", "items", "entries", "results"} {
			if v, ok := obj[key]; ok {
				if list, ok := v.([]interface{}); ok {
					return len(list)
				}
			}
		}
		return 1
	}
	return 1
}

func genericNotifierFeedPayload(spec *FeedSpec, result string) (interface{}, int) {
	if spec != nil {
		if extracted, err := extractFeedData(spec, nil, []string{result}); err == nil {
			if extracted != nil && extracted.RootData != nil && extracted.ItemCount > 0 {
				return extracted.RootData, extracted.ItemCount
			}
			return nil, 0
		}
	}
	var feedData interface{}
	if strings.TrimSpace(result) != "" {
		var parsed interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err == nil {
			feedData = map[string]interface{}{"output": parsed}
		}
	}
	return feedData, 0
}
