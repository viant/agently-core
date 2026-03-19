package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/workspace"
	"gopkg.in/yaml.v3"
)

// FeedSpec describes a tool feed loaded from workspace YAML.
type FeedSpec struct {
	ID         string                 `yaml:"id" json:"id"`
	Title      string                 `yaml:"title,omitempty" json:"title,omitempty"`
	Match      FeedMatch              `yaml:"match" json:"match"`
	Activation FeedActivation         `yaml:"activation,omitempty" json:"activation,omitempty"`
	DataSource map[string]interface{} `yaml:"dataSource,omitempty" json:"dataSource,omitempty"`
	UI         interface{}            `yaml:"ui,omitempty" json:"ui,omitempty"`
}

// FeedMatch defines which tool calls trigger this feed.
type FeedMatch struct {
	Service string `yaml:"service" json:"service"`
	Method  string `yaml:"method" json:"method"`
}

// FeedActivation controls how feed data is gathered.
type FeedActivation struct {
	Kind  string `yaml:"kind,omitempty" json:"kind,omitempty"`   // "history" (default) or "tool_call"
	Scope string `yaml:"scope,omitempty" json:"scope,omitempty"` // "last" (default) or "all"
}

// FeedState tracks active feeds for a conversation.
type FeedState struct {
	FeedID    string `json:"feedId"`
	Title     string `json:"title"`
	ItemCount int    `json:"itemCount"`
	ToolName  string `json:"toolName,omitempty"`
}

// FeedRegistry loads feed specs from workspace and matches tool calls.
type FeedRegistry struct {
	mu    sync.RWMutex
	specs []*FeedSpec
}

// NewFeedRegistry creates a registry and loads all feed specs from workspace.
func NewFeedRegistry() *FeedRegistry {
	r := &FeedRegistry{}
	r.loadFromWorkspace()
	fmt.Printf("[feed-registry] loaded %d feed specs from %s\n", len(r.specs), filepath.Join(workspace.Root(), "feeds"))
	for _, s := range r.specs {
		fmt.Printf("[feed-registry]   %s: match=%s/%s\n", s.ID, s.Match.Service, s.Match.Method)
	}
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
		data, err := os.ReadFile(filepath.Join(feedsDir, entry.Name()))
		if err != nil {
			continue
		}
		var spec FeedSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
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
				spec.Title = strings.Title(spec.ID)
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
	// Normalize: some stores use underscores/hyphens instead of slashes.
	// Convert "system_exec-execute" → "system/exec/execute" before splitting.
	// Replace underscore with slash first (service separator), then split on last slash.
	normalized = strings.ReplaceAll(normalized, "_", "/")
	// Handle "service/method" — split on last slash.
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	if idx := strings.Index(normalized, ":"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	// Try splitting on hyphen for "prefix-method" pattern.
	if idx := strings.Index(normalized, "-"); idx >= 0 {
		return normalized[:idx], normalized[idx+1:]
	}
	return normalized, "*"
}

// EmitFeedActive publishes a tool_feed_active SSE event with the tool result data.
func EmitFeedActive(ctx context.Context, bus streaming.Bus, convID, turnID string, spec *FeedSpec, itemCount int, data interface{}) {
	if bus == nil || spec == nil || convID == "" {
		return
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		TurnID:         turnID,
		Type:           streaming.EventTypeToolFeedActive,
		FeedID:         spec.ID,
		FeedTitle:      spec.Title,
		FeedItemCount:  itemCount,
		FeedData:       data,
		CreatedAt:      time.Now(),
	}
	_ = bus.Publish(ctx, event)
}

// EmitFeedInactive publishes a tool_feed_inactive SSE event.
func EmitFeedInactive(ctx context.Context, bus streaming.Bus, convID string, feedID string) {
	if bus == nil || convID == "" || feedID == "" {
		return
	}
	event := &streaming.Event{
		StreamID:       convID,
		ConversationID: convID,
		Type:           streaming.EventTypeToolFeedInactive,
		FeedID:         feedID,
		CreatedAt:      time.Now(),
	}
	_ = bus.Publish(ctx, event)
}
