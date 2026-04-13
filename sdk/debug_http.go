package sdk

import (
	"context"
	"net/http"
	"strings"

	"github.com/viant/agently-core/internal/logx"
)

const (
	HeaderDebugEnabled    = "X-Agently-Debug"
	HeaderDebugLevel      = "X-Agently-Debug-Level"
	HeaderDebugComponents = "X-Agently-Debug-Components"
)

type SessionDebugConfig struct {
	Enabled    bool
	Level      string
	Components []string
}

func (c *SessionDebugConfig) normalizedLevel() logx.Level {
	if c == nil {
		return logx.LevelDebug
	}
	switch strings.ToLower(strings.TrimSpace(c.Level)) {
	case "error":
		return logx.LevelError
	case "warn", "warning":
		return logx.LevelWarn
	case "info":
		return logx.LevelInfo
	case "trace":
		return logx.LevelTrace
	case "debug", "", "1", "true", "yes", "y", "on":
		return logx.LevelDebug
	default:
		return logx.LevelDebug
	}
}

func (c *SessionDebugConfig) normalizedComponents() []string {
	if c == nil || len(c.Components) == 0 {
		return nil
	}
	result := make([]string, 0, len(c.Components))
	for _, component := range c.Components {
		if trimmed := strings.TrimSpace(component); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func debugContextFromHeaders(ctx context.Context, r *http.Request) context.Context {
	if r == nil {
		return ctx
	}
	enabled := strings.TrimSpace(r.Header.Get(HeaderDebugEnabled))
	level := strings.TrimSpace(r.Header.Get(HeaderDebugLevel))
	componentsRaw := strings.TrimSpace(r.Header.Get(HeaderDebugComponents))
	if enabled == "" && level == "" && componentsRaw == "" {
		return ctx
	}

	shouldEnable := enabled == ""
	switch strings.ToLower(enabled) {
	case "", "1", "true", "yes", "y", "on", "debug":
		shouldEnable = true
	case "0", "false", "no", "n", "off":
		shouldEnable = false
	default:
		shouldEnable = true
	}
	if !shouldEnable {
		return ctx
	}

	var components []string
	if componentsRaw != "" {
		components = strings.Split(componentsRaw, ",")
	}
	cfg := (&SessionDebugConfig{Level: level, Components: components})
	return logx.WithDebugConfig(ctx, cfg.normalizedLevel(), cfg.normalizedComponents()...)
}
