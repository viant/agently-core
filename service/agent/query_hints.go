package agent

import "context"

type freshEmbeddedConversationKey struct{}

func WithFreshEmbeddedConversation(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, freshEmbeddedConversationKey{}, true)
}

func isFreshEmbeddedConversation(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	flag, _ := ctx.Value(freshEmbeddedConversationKey{}).(bool)
	return flag
}
