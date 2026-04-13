package sdk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/sdkapi"
	"github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type FeedSpec = sdkapi.FeedSpec
type FeedMatch = sdkapi.FeedMatch
type FeedActivation = sdkapi.FeedActivation
type FeedState = sdkapi.FeedState

// FeedRegistry loads feed specs from workspace and matches tool calls.
type FeedRegistry struct {
	mu    sync.RWMutex
	specs []*FeedSpec
}

// NewFeedRegistry creates a registry and loads all feed specs from workspace.
func NewFeedRegistry() *FeedRegistry {
	r := &FeedRegistry{}
	r.loadFromWorkspace()
	return r
}

func (r *FeedRegistry) loadFromWorkspace() {
	feedsDir := filepath.Join(workspace.Root(), "feeds")
	entries, err := os.ReadDir(feedsDir)
	if err != nil {
		return
	}
	var specs []*FeedSpec
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		var spec FeedSpec
		if err := wscodec.DecodeFile(filepath.Join(feedsDir, entry.Name()), &spec); err != nil {
			continue
		}
		if spec.ID == "" {
			spec.ID = strings.TrimSuffix(entry.Name(), ".yaml")
		}
		if spec.Title == "" {
			// Derive title from UI section if present
			if ui, ok := spec.UI.(map[string]interface{}); ok {
				if t, ok := ui["title"].(string); ok {
					spec.Title = t
				}
			}
			if spec.Title == "" {
				spec.Title = cases.Title(language.English).String(spec.ID)
			}
		}
		specs = append(specs, &spec)
	}
	r.mu.Lock()
	r.specs = specs
	r.mu.Unlock()
}

// Reload reloads all feed specs from workspace. Safe for hot-swap.
func (r *FeedRegistry) Reload() {
	r.loadFromWorkspace()
}

// Specs returns all loaded feed specs.
func (r *FeedRegistry) Specs() []*FeedSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*FeedSpec, len(r.specs))
	copy(out, r.specs)
	return out
}

// Match returns feed specs that match a tool name (service/method or service:method).
func (r *FeedRegistry) Match(toolName string) []*FeedSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, method := parseToolName(toolName)
	var matched []*FeedSpec
	for _, spec := range r.specs {
		if matchesRule(spec.Match, service, method) {
			matched = append(matched, spec)
		}
	}
	return matched
}

// MatchAny returns true if any feed spec matches the tool name.
func (r *FeedRegistry) MatchAny(toolName string) bool {
	return len(r.Match(toolName)) > 0
}

func matchesRule(m FeedMatch, service, method string) bool {
	svc := strings.ToLower(strings.TrimSpace(m.Service))
	mtd := strings.ToLower(strings.TrimSpace(m.Method))
	if svc == "" {
		return false
	}
	if svc != "*" && svc != service {
		return false
	}
	if mtd != "" && mtd != "*" && mtd != method {
		return false
	}
	return true
}

func parseToolName(name string) (string, string) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "_", "/")
	if idx := strings.Index(normalized, ":"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	// Handle names like "system_patch-apply" and "system/patch-apply" where the
	// final hyphen separates service path from method.
	if slash := strings.LastIndex(normalized, "/"); slash >= 0 {
		if dash := strings.LastIndex(normalized, "-"); dash > slash {
			return normalized[:dash], normalized[dash+1:]
		}
	}
	// Handle "service/method" — split on last slash.
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	// Try splitting on hyphen for "prefix-method" pattern.
	if idx := strings.Index(normalized, "-"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	return normalized, "*"
}

// emitFeedActive publishes a tool_feed_active SSE event with the tool result data.
func emitFeedActive(ctx context.Context, bus streaming.Bus, convID, turnID string, spec *FeedSpec, itemCount int, data interface{}) {
	if bus == nil || spec == nil || convID == "" {
		return
	}
	messageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	if messageID == "" {
		messageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		TurnID:         turnID,
		MessageID:      messageID,
		Type:           streaming.EventTypeToolFeedActive,
		FeedID:         spec.ID,
		FeedTitle:      spec.Title,
		FeedItemCount:  itemCount,
		FeedData:       data,
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(convID, turnID)
	_ = bus.Publish(ctx, event)
}

// emitFeedInactive publishes a tool_feed_inactive SSE event.
func emitFeedInactive(ctx context.Context, bus streaming.Bus, convID string, feedID string) {
	if bus == nil || convID == "" || feedID == "" {
		return
	}
	messageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	if messageID == "" {
		messageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	turnID := ""
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(turn.TurnID)
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		TurnID:         turnID,
		MessageID:      messageID,
		Type:           streaming.EventTypeToolFeedInactive,
		FeedID:         feedID,
		CreatedAt:      time.Now(),
	}
	event.NormalizeIdentity(convID, turnID)
	_ = bus.Publish(ctx, event)
}
