package sdk

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"

	"github.com/viant/agently-core/runtime/memory"
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
	convID := memory.ConversationIDFromContext(ctx)
	turnID := ""
	if turn, ok := memory.TurnMetaFromContext(ctx); ok {
		turnID = turn.TurnID
		if convID == "" {
			convID = turn.ConversationID
		}
	}
	if convID == "" {
		return
	}

	itemCount := estimateItemCount(result)
	// Parse tool result as JSON to include in the SSE event.
	var feedData interface{}
	if strings.TrimSpace(result) != "" {
		var parsed interface{}
		if err := json.Unmarshal([]byte(result), &parsed); err == nil {
			feedData = map[string]interface{}{"output": parsed}
		}
	}
	for _, spec := range matched {
		n.mu.Lock()
		feedsByConversation := n.activeFeeds[convID]
		if feedsByConversation == nil {
			feedsByConversation = map[string]bool{}
			n.activeFeeds[convID] = feedsByConversation
		}
		feedsByConversation[spec.ID] = true
		n.mu.Unlock()
		EmitFeedActive(ctx, n.bus, convID, turnID, spec, itemCount, feedData)
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
			EmitFeedInactive(ctx, n.bus, convID, feedID)
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
