package tool

import (
	"context"
	"strings"
	"sync"

	mcpname "github.com/viant/agently-core/pkg/mcpname"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

type asyncConfigKeyT struct{}

type asyncConfigState struct {
	mu    sync.RWMutex
	tools map[string]*asynccfg.Config
}

var asyncConfigKey = asyncConfigKeyT{}

func WithAsyncConfigState(ctx context.Context) context.Context {
	if asyncConfigFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, asyncConfigKey, &asyncConfigState{tools: map[string]*asynccfg.Config{}})
}

func MarkAsyncTool(ctx context.Context, name string, cfg *asynccfg.Config) {
	state := asyncConfigFromContext(ctx)
	if state == nil || cfg == nil {
		return
	}
	key := normalizedAsyncKey(name)
	if key == "" {
		return
	}
	state.mu.Lock()
	state.tools[key] = cfg
	state.mu.Unlock()
}

func AsyncConfigFor(ctx context.Context, name string) (*asynccfg.Config, bool) {
	state := asyncConfigFromContext(ctx)
	if state == nil {
		return nil, false
	}
	key := normalizedAsyncKey(name)
	if key == "" {
		return nil, false
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	cfg, ok := state.tools[key]
	return cfg, ok && cfg != nil
}

func asyncConfigFromContext(ctx context.Context) *asyncConfigState {
	if ctx == nil {
		return nil
	}
	value := ctx.Value(asyncConfigKey)
	if value == nil {
		return nil
	}
	state, _ := value.(*asyncConfigState)
	return state
}

func normalizedAsyncKey(name string) string {
	return strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
}
