package sdk

import (
	"context"
	"strings"

	"github.com/viant/agently-core/internal/logx"
)

type DebugOption func(*debugOptions)

type debugOptions struct {
	level      logx.Level
	components []string
}

func WithDebugLevel(level string) DebugOption {
	return func(o *debugOptions) {
		switch strings.ToLower(strings.TrimSpace(level)) {
		case "error":
			o.level = logx.LevelError
		case "warn", "warning":
			o.level = logx.LevelWarn
		case "info":
			o.level = logx.LevelInfo
		case "trace":
			o.level = logx.LevelTrace
		case "debug", "", "1", "true", "yes", "y", "on":
			o.level = logx.LevelDebug
		default:
			o.level = logx.LevelDebug
		}
	}
}

func WithDebugComponents(components ...string) DebugOption {
	return func(o *debugOptions) {
		o.components = append(o.components, components...)
	}
}

// WithDebug enables debug logging for a single SDK call context. When no
// components are provided, all components are enabled for that context.
func WithDebug(ctx context.Context, components ...string) context.Context {
	return logx.WithDebugConfig(ctx, logx.LevelDebug, components...)
}

// WithDebugOptions enables debug logging for a single SDK call context using
// explicit options such as level and component filters.
func WithDebugOptions(ctx context.Context, opts ...DebugOption) context.Context {
	cfg := &debugOptions{level: logx.LevelDebug}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return logx.WithDebugConfig(ctx, cfg.level, cfg.components...)
}
