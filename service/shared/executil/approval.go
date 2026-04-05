package executil

import (
	"context"
	"errors"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/memory"
)

// Sentinel errors for prompt-mode approval outcomes.
var (
	errToolPromptDeclined = errors.New("tool execution declined by user")
	errToolPromptCanceled = errors.New("tool execution canceled by user")
)

// ToolApprovalElicitor creates an inline approval prompt for a tool call and
// waits for the user's decision. It returns the normalized action string:
// "accept", "decline", or "cancel".
type ToolApprovalElicitor interface {
	ElicitToolApproval(ctx context.Context, turn *memory.TurnMeta, toolName string, cfg *llm.ApprovalConfig, args map[string]interface{}) (action string, payload map[string]interface{}, err error)
}

type approvalElicitorKeyT struct{}

var approvalElicitorKey = approvalElicitorKeyT{}

// WithApprovalElicitor attaches a ToolApprovalElicitor to the context so that
// prompt-mode tool approval can reach the elicitation service from within
// ExecuteToolStep without threading a new parameter through the call chain.
func WithApprovalElicitor(ctx context.Context, e ToolApprovalElicitor) context.Context {
	if e == nil {
		return ctx
	}
	return context.WithValue(ctx, approvalElicitorKey, e)
}

func approvalElicitorFromContext(ctx context.Context) ToolApprovalElicitor {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(approvalElicitorKey)
	if v == nil {
		return nil
	}
	e, _ := v.(ToolApprovalElicitor)
	return e
}
