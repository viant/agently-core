package executil

import (
	"context"
	"os"
	"strings"
	"time"
)

// toolExecContext returns a bounded context for tool execution. It uses AGENTLY_TOOLCALL_TIMEOUT
// environment variable when set (e.g., "45s", "2m"), otherwise defaults to 60s.
func toolExecContext(ctx context.Context) (context.Context, context.CancelFunc) {
	const defaultTimeout = 3 * time.Minute
	if d, ok := toolTimeoutFromContext(ctx); ok && d > 0 {
		return context.WithTimeout(ctx, d)
	}
	if v := strings.TrimSpace(os.Getenv("AGENTLY_TOOLCALL_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return context.WithTimeout(ctx, d)
		}
	}
	return context.WithTimeout(ctx, defaultTimeout)
}

// ---------------- context helpers ----------------

type ctxKey int

const (
	keyToolTimeout ctxKey = iota + 1
	keyChainMode
	keyWorkdir
	keyFeedNotifier
)

// FeedNotifier is called after a tool completes to check if a feed should be activated.
type FeedNotifier interface {
	// NotifyToolCompleted is called with the tool name and result after execution.
	// Implementations should check if the tool matches any feed spec and emit SSE events.
	NotifyToolCompleted(ctx context.Context, toolName string, result string)
}

// WithFeedNotifier attaches a FeedNotifier to the context.
func WithFeedNotifier(ctx context.Context, n FeedNotifier) context.Context {
	if n == nil {
		return ctx
	}
	return context.WithValue(ctx, keyFeedNotifier, n)
}

func feedNotifierFromContext(ctx context.Context) FeedNotifier {
	if v := ctx.Value(keyFeedNotifier); v != nil {
		if n, ok := v.(FeedNotifier); ok {
			return n
		}
	}
	return nil
}

// WithToolTimeout attaches a per-tool execution timeout to the context.
func WithToolTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, keyToolTimeout, d)
}

// toolTimeoutFromContext reads a configured tool timeout from context when present.
func toolTimeoutFromContext(ctx context.Context) (time.Duration, bool) {
	if v := ctx.Value(keyToolTimeout); v != nil {
		if d, ok := v.(time.Duration); ok {
			return d, true
		}
	}
	return 0, false
}

// WithChainMode marks tool/message execution context as internal chain execution.
func WithChainMode(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, keyChainMode, enabled)
}

// IsChainMode reports whether current execution is internal chain execution.
func IsChainMode(ctx context.Context) bool {
	if v := ctx.Value(keyChainMode); v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// WithWorkdir attaches a resolved default workdir to the execution context.
func WithWorkdir(ctx context.Context, workdir string) context.Context {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return ctx
	}
	return context.WithValue(ctx, keyWorkdir, workdir)
}

// WorkdirFromContext returns the resolved default workdir from context.
func WorkdirFromContext(ctx context.Context) (string, bool) {
	if v := ctx.Value(keyWorkdir); v != nil {
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s, true
			}
		}
	}
	return "", false
}
