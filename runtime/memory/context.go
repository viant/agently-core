package memory

import (
	"context"

	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// Deprecated: package memory is a compatibility shim over runtime/requestctx.
// New code should import runtime/requestctx directly.
var ConversationIDKey = runtimerequestctx.ConversationIDKey

func ConversationIDFromContext(ctx context.Context) string {
	return runtimerequestctx.ConversationIDFromContext(ctx)
}

func WithConversationID(ctx context.Context, conversationID string) context.Context {
	return runtimerequestctx.WithConversationID(ctx, conversationID)
}
