package approvalqueue

import (
	"context"
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

type stateKey struct{}

type State struct {
	mu    sync.RWMutex
	tools map[string]*llm.ApprovalConfig
}

// WithState ensures a mutable approval-queue state exists in context.
func WithState(ctx context.Context) context.Context {
	if StateFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, stateKey{}, &State{tools: map[string]*llm.ApprovalConfig{}})
}

// StateFromContext returns the approval-queue state when present.
func StateFromContext(ctx context.Context) *State {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(stateKey{})
	if v == nil {
		return nil
	}
	st, _ := v.(*State)
	return st
}

// MarkTool attaches approval configuration to a tool name.
func MarkTool(ctx context.Context, name string, cfg *llm.ApprovalConfig) {
	st := StateFromContext(ctx)
	if st == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
	if key == "" {
		return
	}
	if cfg == nil {
		cfg = &llm.ApprovalConfig{Mode: llm.ApprovalModeQueue}
	}
	if !cfg.IsQueue() && !cfg.IsPrompt() {
		return
	}
	st.mu.Lock()
	st.tools[key] = cfg
	st.mu.Unlock()
}

// RequiresQueue reports whether a tool should be queued for approval.
func RequiresQueue(ctx context.Context, name string) bool {
	cfg, ok := ConfigFor(ctx, name)
	return ok && cfg.IsQueue()
}

// RequiresPrompt reports whether a tool requires prompt-mode approval.
func RequiresPrompt(ctx context.Context, name string) bool {
	cfg, ok := ConfigFor(ctx, name)
	return ok && cfg.IsPrompt()
}

// ConfigFor returns the approval config for a tool.
func ConfigFor(ctx context.Context, name string) (*llm.ApprovalConfig, bool) {
	st := StateFromContext(ctx)
	if st == nil {
		return nil, false
	}
	key := strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
	if key == "" {
		return nil, false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	cfg, ok := st.tools[key]
	return cfg, ok && cfg != nil
}
