package asyncconfig

import (
	"context"
	"strings"
	"sync"

	mcpname "github.com/viant/agently-core/pkg/mcpname"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

type stateKey struct{}

type State struct {
	mu    sync.RWMutex
	tools map[string]*asynccfg.Config
}

// WithState ensures a mutable async-config state exists in context.
func WithState(ctx context.Context) context.Context {
	if StateFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &State{tools: map[string]*asynccfg.Config{}})
}

// StateFromContext returns async-config state when present.
func StateFromContext(ctx context.Context) *State {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(stateKey{})
	if value == nil {
		return nil
	}
	state, _ := value.(*State)
	return state
}

// MarkTool records async config for a tool.
func MarkTool(ctx context.Context, name string, cfg *asynccfg.Config) {
	state := StateFromContext(ctx)
	if state == nil || cfg == nil {
		return
	}
	key := normalizedKey(name)
	if key == "" {
		return
	}
	state.mu.Lock()
	state.tools[key] = cfg
	state.mu.Unlock()
}

// ConfigFor returns async config for a tool.
func ConfigFor(ctx context.Context, name string) (*asynccfg.Config, bool) {
	state := StateFromContext(ctx)
	if state == nil {
		return nil, false
	}
	key := normalizedKey(name)
	if key == "" {
		return nil, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	cfg, ok := state.tools[key]
	return cfg, ok && cfg != nil
}

func normalizedKey(name string) string {
	return strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
}
