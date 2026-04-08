package memory

import (
	"context"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Deprecated: package memory is a compatibility shim over runtime/requestctx.
// New code should import runtime/requestctx directly.
type TurnMeta = runtimerequestctx.TurnMeta
type ModelCompletionMeta = runtimerequestctx.ModelCompletionMeta
type EmbedFunc = func(ctx context.Context, texts []string) ([][]float32, error)

var ModelMessageIDKey = runtimerequestctx.ModelMessageIDKey
var ToolMessageIDKey = runtimerequestctx.ToolMessageIDKey

func ModelMessageIDFromContext(ctx context.Context) string {
	return runtimerequestctx.ModelMessageIDFromContext(ctx)
}

func WithToolMessageID(ctx context.Context, messageID string) context.Context {
	return runtimerequestctx.WithToolMessageID(ctx, messageID)
}

func ToolMessageIDFromContext(ctx context.Context) string {
	return runtimerequestctx.ToolMessageIDFromContext(ctx)
}

func SetTurnTrace(turnID, traceID string) {
	runtimerequestctx.SetTurnTrace(turnID, traceID)
}

func TurnTrace(turnID string) string {
	return runtimerequestctx.TurnTrace(turnID)
}

func SetTurnModelMessageID(turnID, msgID string) {
	runtimerequestctx.SetTurnModelMessageID(turnID, msgID)
}

func TurnModelMessageID(turnID string) string {
	return runtimerequestctx.TurnModelMessageID(turnID)
}

func CleanupTurn(turnID string) {
	runtimerequestctx.CleanupTurn(turnID)
}

func WithTurnMeta(ctx context.Context, meta TurnMeta) context.Context {
	return runtimerequestctx.WithTurnMeta(ctx, meta)
}

func TurnMetaFromContext(ctx context.Context) (TurnMeta, bool) {
	return runtimerequestctx.TurnMetaFromContext(ctx)
}

func WithModelCompletionMeta(ctx context.Context, meta ModelCompletionMeta) context.Context {
	return runtimerequestctx.WithModelCompletionMeta(ctx, meta)
}

func ModelCompletionMetaFromContext(ctx context.Context) (ModelCompletionMeta, bool) {
	return runtimerequestctx.ModelCompletionMetaFromContext(ctx)
}

func WithRequestMode(ctx context.Context, mode string) context.Context {
	return runtimerequestctx.WithRequestMode(ctx, mode)
}

func RequestModeFromContext(ctx context.Context) string {
	return runtimerequestctx.RequestModeFromContext(ctx)
}
