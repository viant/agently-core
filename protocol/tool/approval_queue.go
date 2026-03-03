package tool

import (
	"context"
	"strings"
	"sync"

	mcpname "github.com/viant/agently-core/pkg/mcpname"
)

type approvalQueueKeyT struct{}

type approvalQueueState struct {
	mu    sync.RWMutex
	tools map[string]*ApprovalQueueConfig
}

type ApprovalQueueConfig struct {
	Enabled            bool
	TitleSelector      string
	DataSourceSelector string
	UIURI              string
}

var approvalQueueKey = approvalQueueKeyT{}

// WithApprovalQueueState ensures a mutable approval-queue state exists in context.
func WithApprovalQueueState(ctx context.Context) context.Context {
	if approvalQueueFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, approvalQueueKey, &approvalQueueState{tools: map[string]*ApprovalQueueConfig{}})
}

// MarkApprovalQueueTool marks a tool name to require approval queue.
func MarkApprovalQueueTool(ctx context.Context, name string, cfg *ApprovalQueueConfig) {
	st := approvalQueueFromContext(ctx)
	if st == nil {
		return
	}
	key := strings.ToLower(strings.TrimSpace(mcpname.Canonical(name)))
	if key == "" {
		return
	}
	if cfg == nil {
		cfg = &ApprovalQueueConfig{Enabled: true}
	}
	if !cfg.Enabled {
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
	return ok && cfg != nil && cfg.Enabled
}

// ApprovalQueueFor returns the approval queue config for a tool.
func ApprovalQueueFor(ctx context.Context, name string) (*ApprovalQueueConfig, bool) {
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
