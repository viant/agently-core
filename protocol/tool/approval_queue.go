package tool

import (
	"context"
	"strings"
	"sync"

	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

type approvalQueueKeyT struct{}

type approvalQueueState struct {
	mu    sync.RWMutex
	tools map[string]*llm.ApprovalConfig
}

var approvalQueueKey = approvalQueueKeyT{}

// WithApprovalQueueState ensures a mutable approval-queue state exists in context.
func WithApprovalQueueState(ctx context.Context) context.Context {
	if approvalQueueFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, approvalQueueKey, &approvalQueueState{tools: map[string]*llm.ApprovalConfig{}})
}

// MarkApprovalQueueTool marks a tool name to require approval queue.
func MarkApprovalQueueTool(ctx context.Context, name string, cfg *llm.ApprovalConfig) {
	st := approvalQueueFromContext(ctx)
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
	if !cfg.IsQueue() {
		return
	}
	st.mu.Lock()
	st.tools[key] = cfg
	st.mu.Unlock()
}

// RequiresApprovalQueue reports whether a tool should be queued for approval.
func RequiresApprovalQueue(ctx context.Context, name string) bool {
	st := approvalQueueFromContext(ctx)
	if st == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
	if key == "" {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	cfg, ok := st.tools[key]
	return ok && cfg.IsQueue()
}

// RequiresPromptApproval reports whether a tool requires prompt-mode approval.
func RequiresPromptApproval(ctx context.Context, name string) bool {
	cfg, ok := ApprovalQueueFor(ctx, name)
	return ok && cfg.IsPrompt()
}

// ApprovalQueueFor returns the approval config for a tool.
func ApprovalQueueFor(ctx context.Context, name string) (*llm.ApprovalConfig, bool) {
	st := approvalQueueFromContext(ctx)
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

func approvalQueueFromContext(ctx context.Context) *approvalQueueState {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(approvalQueueKey)
	if v == nil {
		return nil
	}
	st, _ := v.(*approvalQueueState)
	return st
}
