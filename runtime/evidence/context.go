// Package evidence carries server-installed, turn-scoped evidence boundaries.
// It has no dependency on a business policy or tool execution implementation.
package evidence

import (
	"context"
	"encoding/json"
)

// Tools prepares the exact effective request before any durable capture and
// projects a result only after the executor confirms its durable completion.
// Implementations must revalidate the authenticated turn on every invocation.
type Tools interface {
	Prepare(context.Context, string, string, json.RawMessage) (json.RawMessage, bool, error)
	Completed(context.Context, string, string) (json.RawMessage, bool, error)
}

type toolsKey struct{}

// WithTools installs a trusted host capability; JSON input cannot install one.
func WithTools(ctx context.Context, tools Tools) context.Context {
	return context.WithValue(ctx, toolsKey{}, tools)
}

func ToolsFromContext(ctx context.Context) Tools {
	tools, _ := ctx.Value(toolsKey{}).(Tools)
	return tools
}
