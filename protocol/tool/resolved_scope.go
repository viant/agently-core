package tool

import (
	"context"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/pkg/mcpname"
)

type resolvedToolScopeKeyT struct{}

var resolvedToolScopeKey = resolvedToolScopeKeyT{}

type resolvedToolScope struct {
	names map[string]struct{}
}

// WithResolvedToolDefinitions records the concrete tool definitions exposed to
// the model for the current turn. An empty definition list is still a scoped
// turn: no model-emitted tool calls are executable.
func WithResolvedToolDefinitions(ctx context.Context, defs []*llm.ToolDefinition) context.Context {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		names = append(names, def.Name)
	}
	return WithResolvedToolNames(ctx, names)
}

// WithResolvedToolNames records the concrete executable tool names for a turn.
func WithResolvedToolNames(ctx context.Context, names []string) context.Context {
	scope := &resolvedToolScope{names: map[string]struct{}{}}
	for _, name := range names {
		if canonical := canonicalResolvedToolName(name); canonical != "" {
			scope.names[canonical] = struct{}{}
		}
	}
	return context.WithValue(ctx, resolvedToolScopeKey, scope)
}

// ResolvedToolAllowed reports whether name was exposed in the resolved tool
// signatures for this turn. The second return value is false when no resolved
// scope was recorded on the context.
func ResolvedToolAllowed(ctx context.Context, name string) (bool, bool) {
	scope, ok := ctx.Value(resolvedToolScopeKey).(*resolvedToolScope)
	if !ok || scope == nil {
		return true, false
	}
	canonical := canonicalResolvedToolName(name)
	_, ok = scope.names[canonical]
	return ok, true
}

func canonicalResolvedToolName(name string) string {
	return strings.ToLower(strings.TrimSpace(mcpname.Canonical(strings.TrimSpace(name))))
}
