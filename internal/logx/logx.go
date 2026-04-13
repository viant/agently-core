package logx

import (
	"context"
	"log"
	"os"
	"strings"
)

type Level int

type debugContextKey struct{}

type DebugContext struct {
	Enabled    bool
	Level      Level
	Components map[string]struct{}
}

const (
	LevelOff Level = iota
	LevelError
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

func Enabled() bool {
	return CurrentLevel() > LevelOff
}

func EnabledFor(keys ...string) bool {
	if Enabled() {
		return true
	}
	for _, key := range keys {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "1", "true", "yes", "y", "on":
			return true
		}
	}
	return false
}

func CurrentLevel() Level {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTLY_DEBUG")))
	switch value {
	case "", "0", "false", "no", "n", "off":
		return LevelOff
	case "error":
		return LevelError
	case "warn", "warning":
		return LevelWarn
	case "info":
		return LevelInfo
	case "trace":
		return LevelTrace
	case "1", "true", "yes", "y", "on", "debug":
		return LevelDebug
	default:
		// Unknown non-empty values still enable debug to keep activation simple.
		return LevelDebug
	}
}

func EnabledAt(level Level) bool {
	return CurrentLevel() >= level
}

func WithDebug(ctx context.Context, components ...string) context.Context {
	return WithDebugConfig(ctx, LevelDebug, components...)
}

func WithDebugConfig(ctx context.Context, level Level, components ...string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if level <= LevelOff {
		level = LevelDebug
	}
	cfg := &DebugContext{Enabled: true, Level: level}
	if len(components) > 0 {
		cfg.Components = make(map[string]struct{}, len(components))
		for _, component := range components {
			component = strings.ToLower(strings.TrimSpace(component))
			if component == "" {
				continue
			}
			cfg.Components[component] = struct{}{}
		}
	}
	return context.WithValue(ctx, debugContextKey{}, cfg)
}

func debugContextFrom(ctx context.Context) *DebugContext {
	if ctx == nil {
		return nil
	}
	cfg, _ := ctx.Value(debugContextKey{}).(*DebugContext)
	return cfg
}

func componentEnabled(component string) bool {
	filters := strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_COMPONENTS"))
	if filters == "" {
		return true
	}
	component = strings.ToLower(strings.TrimSpace(component))
	if component == "" {
		return true
	}
	for _, entry := range strings.Split(filters, ",") {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if entry == component {
			return true
		}
	}
	return false
}

func componentEnabledWithContext(ctx context.Context, component string) bool {
	component = strings.ToLower(strings.TrimSpace(component))
	if component == "" {
		return true
	}
	if cfg := debugContextFrom(ctx); cfg != nil && cfg.Enabled {
		if len(cfg.Components) == 0 {
			return true
		}
		_, ok := cfg.Components[component]
		return ok
	}
	return componentEnabled(component)
}

func enabledAtWithContext(ctx context.Context, level Level) bool {
	if cfg := debugContextFrom(ctx); cfg != nil && cfg.Enabled {
		ctxLevel := cfg.Level
		if ctxLevel <= LevelOff {
			ctxLevel = LevelDebug
		}
		return ctxLevel >= level
	}
	return EnabledAt(level)
}

// EnabledAtWithContextForTest exposes context-scoped level evaluation for
// package-internal tests that need to verify request-scoped debug wiring.
func EnabledAtWithContextForTest(ctx context.Context, level Level) bool {
	return enabledAtWithContext(ctx, level)
}

// ComponentEnabledWithContextForTest exposes component filtering for
// package-internal tests that need to verify request-scoped debug wiring.
func ComponentEnabledWithContextForTest(ctx context.Context, component string) bool {
	return componentEnabledWithContext(ctx, component)
}

func Debugf(component, format string, args ...any) {
	if !EnabledAt(LevelDebug) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabled(component) {
		return
	}
	log.Printf("[debug][%s] "+format, append([]any{component}, args...)...)
}

func DebugCtxf(ctx context.Context, component, format string, args ...any) {
	if !enabledAtWithContext(ctx, LevelDebug) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabledWithContext(ctx, component) {
		return
	}
	log.Printf("[debug][%s] "+format, append([]any{component}, args...)...)
}

func Infof(component, format string, args ...any) {
	if !EnabledAt(LevelInfo) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabled(component) {
		return
	}
	log.Printf("[debug][%s] [INFO] "+format, append([]any{component}, args...)...)
}

func InfoCtxf(ctx context.Context, component, format string, args ...any) {
	if !enabledAtWithContext(ctx, LevelInfo) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabledWithContext(ctx, component) {
		return
	}
	log.Printf("[debug][%s] [INFO] "+format, append([]any{component}, args...)...)
}

func Warnf(component, format string, args ...any) {
	if !EnabledAt(LevelWarn) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabled(component) {
		return
	}
	log.Printf("[debug][%s] [WARN] "+format, append([]any{component}, args...)...)
}

func WarnCtxf(ctx context.Context, component, format string, args ...any) {
	if !enabledAtWithContext(ctx, LevelWarn) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabledWithContext(ctx, component) {
		return
	}
	log.Printf("[debug][%s] [WARN] "+format, append([]any{component}, args...)...)
}

func Errorf(component, format string, args ...any) {
	if !EnabledAt(LevelError) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabled(component) {
		return
	}
	log.Printf("[debug][%s] [ERROR] "+format, append([]any{component}, args...)...)
}

func ErrorCtxf(ctx context.Context, component, format string, args ...any) {
	if !enabledAtWithContext(ctx, LevelError) {
		return
	}
	component = strings.TrimSpace(component)
	if component == "" {
		component = "debug"
	}
	if !componentEnabledWithContext(ctx, component) {
		return
	}
	log.Printf("[debug][%s] [ERROR] "+format, append([]any{component}, args...)...)
}
