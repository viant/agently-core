package toolapproval

import (
	"context"

	"github.com/viant/agently-core/genai/llm"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Elicitor creates an inline approval prompt for a tool call and waits for the
// user's decision. It returns the normalized action string: accept, decline, or cancel.
type Elicitor interface {
	ElicitToolApproval(ctx context.Context, turn *runtimerequestctx.TurnMeta, toolName string, cfg *llm.ApprovalConfig, args map[string]interface{}) (action string, payload map[string]interface{}, err error)
}

type contextKey struct{}

func WithElicitor(ctx context.Context, e Elicitor) context.Context {
	if e == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, e)
}

func ElicitorFromContext(ctx context.Context) Elicitor {
	if ctx == nil {
		return nil
	}
	v := ctx.Value(contextKey{})
	if v == nil {
		return nil
	}
	e, _ := v.(Elicitor)
	return e
}
