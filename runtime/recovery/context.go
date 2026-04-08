package recovery

import (
	"context"
	"strings"
)

type modeKey struct{}

const (
	ModeCompact      = "compact"
	ModePruneCompact = "pruneCompact"
)

func WithMode(ctx context.Context, mode string) context.Context {
	m := strings.TrimSpace(mode)
	if m == "" {
		return ctx
	}
	return context.WithValue(ctx, modeKey{}, m)
}

func ModeFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	if v, ok := ctx.Value(modeKey{}).(string); ok {
		return strings.TrimSpace(v), true
	}
	return "", false
}
