// Package wsconfig applies operator-tunable async defaults to the
// Manager + narrator package. Operators configure these values in the
// workspace-level `config.yaml` under `default.async`; this package is
// the thin adapter that turns parsed duration strings into calls to
// `Manager.StartGC` and `narrator.SetLLMTimeout`.
//
// Kept in its own subpackage to avoid an import cycle: the narrator
// package itself imports `protocol/async`, so any config-loading code
// that touches both must sit outside `protocol/async`.
package wsconfig

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	asynccfg "github.com/viant/agently-core/protocol/async"
	asyncnarrator "github.com/viant/agently-core/protocol/async/narrator"
)

// WorkspaceConfig mirrors the YAML surface for operator-tunable async
// defaults. It is populated from the workspace `config.yaml` under
// `default.async`. Zero / empty values fall back to built-in package
// defaults at the time `Apply` runs.
//
// Example (excerpt from `config.yaml`):
//
//	default:
//	  model: openai_gpt-5_4
//	  async:
//	    gc:
//	      interval: 5m
//	      maxAge:   1h
//	    narrator:
//	      llmTimeout: 3s
//
// Durations parse via time.ParseDuration ("500ms", "10s", "5m", "1h").
type WorkspaceConfig struct {
	GC       *GCConfig       `json:"gc,omitempty" yaml:"gc,omitempty"`
	Narrator *NarratorConfig `json:"narrator,omitempty" yaml:"narrator,omitempty"`
}

type GCConfig struct {
	Interval string `json:"interval,omitempty" yaml:"interval,omitempty"`
	MaxAge   string `json:"maxAge,omitempty" yaml:"maxAge,omitempty"`
}

type NarratorConfig struct {
	// LLMTimeout bounds how long the barrier event loop waits on an LLM
	// narrator runner per invocation when no ctx-scoped override is in
	// effect. Parsed via time.ParseDuration (e.g. "3s", "500ms").
	LLMTimeout string `json:"llmTimeout,omitempty" yaml:"llmTimeout,omitempty"`
	// Prompt is the system prompt used by the narrator LLM runner when
	// Narration mode is "llm". Empty → workspace baseline embedded
	// default applies. Operators can override this in the workspace
	// `config.yaml` under `default.async.narrator.prompt` to steer tone
	// / length / localization.
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`
}

// ParseWorkspaceConfig parses YAML bytes into a WorkspaceConfig. Empty
// input returns an empty config with no error. Useful for unit tests
// and for callers that have already read the YAML bytes themselves.
func ParseWorkspaceConfig(data []byte) (*WorkspaceConfig, error) {
	if len(data) == 0 {
		return &WorkspaceConfig{}, nil
	}
	out := &WorkspaceConfig{}
	if err := yaml.Unmarshal(data, out); err != nil {
		return nil, fmt.Errorf("async workspace config: %w", err)
	}
	return out, nil
}

// Apply resolves the config's duration strings, forwards the narrator
// LLM timeout to the narrator package, and starts the Manager GC loop.
// Returns the resolved GC interval/maxAge/narrator timeout so callers
// can log what was applied.
//
// Behavior:
//   - Nil config or missing fields: nothing is applied for the missing
//     piece; the baseline values that reached this Apply call (from
//     `workspace/config.DefaultsWithFallback`) carry through as-is
//     because the workspace loader pre-populates them.
//   - `Manager.StartGC` is invoked only when both resolved durations
//     are positive — this package holds no defaults of its own; the
//     authoritative defaults live in the workspace `default.async`
//     baseline.
//   - Parse errors surface loudly so operator typos fail at bootstrap
//     rather than silently degrading.
//
// Ctx lifecycle: the GC goroutine runs until ctx is canceled. Pass a
// long-lived application context. When ctx or manager is nil, GC
// is not started.
func (c *WorkspaceConfig) Apply(ctx context.Context, manager *asynccfg.Manager) (gcInterval, gcMaxAge, narratorTimeout time.Duration, err error) {
	if c != nil {
		if c.Narrator != nil {
			if raw := strings.TrimSpace(c.Narrator.LLMTimeout); raw != "" {
				narratorTimeout, err = time.ParseDuration(raw)
				if err != nil {
					return 0, 0, 0, fmt.Errorf("async workspace config: narrator.llmTimeout=%q: %w", raw, err)
				}
				asyncnarrator.SetLLMTimeout(narratorTimeout)
			}
		}
		if c.GC != nil {
			if raw := strings.TrimSpace(c.GC.Interval); raw != "" {
				gcInterval, err = time.ParseDuration(raw)
				if err != nil {
					return 0, 0, 0, fmt.Errorf("async workspace config: gc.interval=%q: %w", raw, err)
				}
			}
			if raw := strings.TrimSpace(c.GC.MaxAge); raw != "" {
				gcMaxAge, err = time.ParseDuration(raw)
				if err != nil {
					return 0, 0, 0, fmt.Errorf("async workspace config: gc.maxAge=%q: %w", raw, err)
				}
			}
		}
	}
	if manager != nil && ctx != nil {
		// StartGC only runs with positive interval+maxAge. Bootstrap
		// seeds both via the workspace baseline; missing/zero values
		// here mean the caller bypassed that seeding — skip silently
		// rather than inventing a default in this package.
		manager.StartGC(ctx, gcInterval, gcMaxAge)
	}
	return gcInterval, gcMaxAge, narratorTimeout, nil
}
