package sdk

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	api "github.com/viant/agently-core/sdk/api"
	"github.com/viant/agently-core/workspace"
	wscodec "github.com/viant/agently-core/workspace/codec"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type FeedSpec = api.FeedSpec
type FeedMatch = api.FeedMatch
type FeedActivation = api.FeedActivation
type FeedPresentation = api.FeedPresentation
type FeedState = api.FeedState

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

// loadFromWorkspace reads every *.yaml under <workspace>/feeds, builds a new
// slice of specs outside any lock, then swaps it in under r.mu. Doing the
// disk I/O without the registry lock held is deliberate — otherwise a reload
// would stall every reader (Specs, MatchByTool, …) for the duration of the
// scan. Two concurrent reloads are safe: both build their own slice and the
// later swap wins.
func (r *FeedRegistry) loadFromWorkspace() {
	feedsDir := filepath.Join(workspace.Root(), "feeds")
	entries, err := os.ReadDir(feedsDir)
	if err != nil {
		log.Printf("[feed-registry] load skipped dir=%q err=%v", feedsDir, err)
		return
	}
	var specs []*FeedSpec
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		var spec FeedSpec
		if err := wscodec.DecodeFile(filepath.Join(feedsDir, entry.Name()), &spec); err != nil {
			log.Printf("[feed-registry] decode skipped file=%q err=%v", filepath.Join(feedsDir, entry.Name()), err)
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
		if spec.Presentation != nil {
			spec.Presentation = normalizedFeedPresentation(&spec)
		}
		specs = append(specs, &spec)
	}
	r.mu.Lock()
	r.specs = specs
	r.mu.Unlock()
	log.Printf("[feed-registry] loaded dir=%q specs=%d", feedsDir, len(specs))
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
	if normalized == "" {
		return "", "*"
	}
	lastSlash := strings.LastIndex(normalized, "/")
	lastDash := strings.LastIndex(normalized, "-")
	lastColon := strings.LastIndex(normalized, ":")
	separator := -1
	// Canonical service/path-method names use a final dash after the service
	// path. Display service/path/method names split on the final slash. A colon
	// is a separator only when no later slash/dash identifies the method.
	switch {
	case lastDash > lastSlash && lastDash > lastColon:
		separator = lastDash
	case lastSlash >= 0:
		separator = lastSlash
	case lastColon >= 0:
		separator = lastColon
	case lastDash >= 0:
		separator = lastDash
	}
	if separator < 0 {
		return normalizeFeedServiceName(normalized), "*"
	}
	service := normalizeFeedServiceName(normalized[:separator])
	method := strings.TrimSpace(normalized[separator+1:])
	if method == "" {
		method = "*"
	}
	return service, method
}

func normalizeFeedServiceName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", "/")
	value = strings.ReplaceAll(value, ":", "/")
	return strings.Trim(value, "/")
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
		StreamID:          convID,
		ConversationID:    convID,
		TurnID:            turnID,
		MessageID:         messageID,
		Type:              streaming.EventTypeToolFeedActive,
		FeedID:            spec.ID,
		FeedTitle:         spec.Title,
		FeedDeveloperOnly: spec.DeveloperOnly,
		FeedIcon:          feedPresentationIcon(spec),
		FeedAccent:        feedPresentationAccent(spec),
		FeedTarget:        feedPresentationTarget(spec),
		FeedItemCount:     itemCount,
		FeedData:          data,
		CreatedAt:         time.Now(),
	}
	event.NormalizeIdentity(convID, turnID)
	_ = bus.Publish(ctx, event)
}

func feedPresentationIcon(f *FeedSpec) string {
	if f == nil || f.Presentation == nil {
		return ""
	}
	return strings.TrimSpace(f.Presentation.Icon)
}

func feedPresentationAccent(f *FeedSpec) string {
	if f == nil || f.Presentation == nil {
		return ""
	}
	return strings.TrimSpace(f.Presentation.Accent)
}

func feedPresentationTarget(f *FeedSpec) string {
	if f == nil || f.Presentation == nil {
		return ""
	}
	return normalizeFeedPresentationTarget(f.Presentation.Target)
}

func normalizedFeedPresentation(f *FeedSpec) *FeedPresentation {
	if f == nil || f.Presentation == nil {
		return nil
	}
	result := *f.Presentation
	result.Icon = strings.TrimSpace(result.Icon)
	result.Accent = strings.TrimSpace(result.Accent)
	result.Target = normalizeFeedPresentationTarget(result.Target)
	seenReportIDs := map[string]bool{}
	result.SuppressReportIDs = result.SuppressReportIDs[:0]
	for _, candidate := range f.Presentation.SuppressReportIDs {
		id := strings.TrimSpace(candidate)
		if id == "" || seenReportIDs[id] {
			continue
		}
		seenReportIDs[id] = true
		result.SuppressReportIDs = append(result.SuppressReportIDs, id)
	}
	if result.Icon == "" && result.Accent == "" && result.Target == "" && len(result.SuppressReportIDs) == 0 {
		return nil
	}
	return &result
}

func normalizeFeedPresentationTarget(value string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "", "auto":
		return normalized
	case "inline", "workspace", "detached":
		return normalized
	default:
		return "auto"
	}
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
