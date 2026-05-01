package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/viant/agently-core/genai/llm"
	provider "github.com/viant/agently-core/genai/llm/provider"
	"github.com/viant/agently-core/internal/matcher"
	"github.com/viant/agently-core/internal/registry"
	"github.com/viant/agently-core/runtime/usage"
)

type Finder struct {
	modelFactory   *provider.Factory
	configRegistry *registry.Registry[*provider.Config]
	configLoader   provider.ConfigLoader
	models         map[string]llm.Model
	mux            sync.RWMutex
	version        int64

	// Candidates cache. Skill activation, intake, and `llm/agents:start`
	// all hit Best/BestWithFilter on the hot path; without this cache
	// each call iterates the full config registry. The cache is keyed by
	// d.version (atomic.Int64) — rebuilds happen only when the registry
	// actually changes (provider added, removed, or replaced). Cache and
	// its version snapshot are guarded by candCacheMux to keep
	// Candidates() concurrent-safe with config-registry mutations.
	candCacheMux     sync.RWMutex
	candCache        []matcher.Candidate
	candCacheVersion int64
	candCacheValid   bool // distinguishes "never built" from "built and empty"
}

// ConfigByIDOrModel returns the provider config matching the given identifier.
// It first attempts a direct lookup by config ID. If not found, it scans all
// configs to find a match either by config ID or by the provider model name
// stored in Options.Model. Returns nil when no matching config exists.
func (d *Finder) ConfigByIDOrModel(id string) *provider.Config {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if cfg, err := d.configRegistry.Lookup(context.Background(), id); err == nil && cfg != nil {
		return cfg
	}
	if all, err := d.configRegistry.List(context.Background()); err == nil {
		for _, cfg := range all {
			if cfg == nil {
				continue
			}
			if strings.EqualFold(cfg.ID, id) || strings.EqualFold(strings.TrimSpace(cfg.Options.Model), strings.TrimSpace(id)) {
				return cfg
			}
		}
	}
	return nil
}

func (d *Finder) Best(p *llm.ModelPreferences) string {
	return d.Matcher().Best(p)
}

// BestWithFilter selects the best model after reducing candidates using allow.
// When the filter excludes all candidates, it falls back to the full set.
func (d *Finder) BestWithFilter(p *llm.ModelPreferences, allow func(id string) bool) string {
	cands := d.Candidates()
	if allow != nil {
		filtered := make([]matcher.Candidate, 0, len(cands))
		for _, c := range cands {
			if allow(c.ID) {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			return matcher.New(filtered).Best(p)
		}
	}
	return matcher.New(cands).Best(p)
}

func (d *Finder) Find(ctx context.Context, id string) (llm.Model, error) {
	d.mux.RLock()
	ret, ok := d.models[id]
	d.mux.RUnlock()
	if ok {
		return ret, nil
	}
	d.mux.Lock()
	defer d.mux.Unlock()
	if ret, ok = d.models[id]; ok {
		return ret, nil
	}
	config, err := d.configRegistry.Lookup(ctx, id)
	if err != nil {
		if d.configLoader != nil {
			config, err = d.configLoader.Load(ctx, id)
			if err != nil {
				fallback := filepath.ToSlash(filepath.Join("models", strings.TrimSpace(id)))
				config, err = d.configLoader.Load(ctx, fallback)
			}
		}
		if err != nil {
			// H1 (modelpref-pkg.md §3): heuristic provider inference is
			// disabled by default. Workspaces that depended on
			// `inferConfigFromID` (e.g. an agent referencing
			// `openai_gpt-5_mini` without a corresponding YAML) opt in via
			// AGENTLY_ALLOW_LEGACY_INFER=1 while migrating to explicit
			// model registrations. Default behavior surfaces a clean
			// ErrModelNotRegistered so missing configs are obvious.
			if legacyInferEnabled() {
				config = inferConfigFromID(id)
			}
			if config == nil {
				return nil, fmt.Errorf("%w: %s", ErrModelNotRegistered, id)
			}
		}
	}
	if config == nil {
		if legacyInferEnabled() {
			config = inferConfigFromID(id)
		}
		if config == nil {
			return nil, fmt.Errorf("%w: %s", ErrModelNotRegistered, id)
		}
	}
	if config != nil && strings.TrimSpace(config.ID) != "" {
		d.configRegistry.Add(config.ID, config)
	}

	// Attach context Usage Aggregator as UsageListener when present and when
	// the config does not already define one.
	if agg := usage.FromContext(ctx); agg != nil {
		if config.Options.UsageListener == nil {
			// Pass method value so it conforms to base.UsageListener (function type)
			config.Options.UsageListener = func(model string, u *llm.Usage) {
				agg.OnUsage(model, u)
			}
		}
	}

	model, err := d.modelFactory.CreateModel(ctx, &config.Options)
	if err != nil {
		return nil, err
	}
	d.models[id] = model
	return model, nil
}

var versionUnderscorePattern = regexp.MustCompile(`-(\d+)-(\d+)(?:$|-)`)

// ErrModelNotRegistered is returned by Finder.Find when no model
// configuration matches the requested id and no registered config can
// be loaded. Wrapped with the offending id via fmt.Errorf("%w: <id>", ...)
// so errors.Is(err, ErrModelNotRegistered) detects the condition while
// the wrapped message still names the missing model.
//
// Per H1 (modelpref-pkg.md §3): the legacy provider-prefix heuristic
// `inferConfigFromID` is opt-in via AGENTLY_ALLOW_LEGACY_INFER=1.
// Default behavior surfaces this typed error so missing model configs
// are caught at the boundary rather than papered over by inference.
// Callers (skill activation, intake, agent.Query) that today rely on
// inference must register an explicit YAML under
// `<workspace>/models/<id>.yaml` or set the env flag during migration.
var ErrModelNotRegistered = errors.New("model not registered")

// legacyInferEnabled returns true when the back-compat escape hatch is
// set. Off by default — callers should register models explicitly.
func legacyInferEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTLY_ALLOW_LEGACY_INFER"))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func inferConfigFromID(id string) *provider.Config {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	switch {
	case strings.HasPrefix(id, "openai_"):
		model, continuation := inferOpenAIModel(strings.TrimPrefix(id, "openai_"))
		if model == "" {
			return nil
		}
		return &provider.Config{
			ID:          id,
			Name:        model + " (OpenAI)",
			Description: "OpenAI " + model + " model",
			Options: provider.Options{
				Provider:            provider.ProviderOpenAI,
				Model:               model,
				EnvKey:              "OPENAI_API_KEY",
				ContextContinuation: continuation,
			},
		}
	default:
		return nil
	}
}

func inferOpenAIModel(raw string) (string, *bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	continuation := (*bool)(nil)
	if strings.HasSuffix(raw, "_responses") {
		raw = strings.TrimSuffix(raw, "_responses")
		v := true
		continuation = &v
	}
	raw = strings.ReplaceAll(raw, "_", "-")
	raw = versionUnderscorePattern.ReplaceAllString(raw, "-$1.$2$2")
	// ReplaceAllString above duplicates the trailing digit in the suffix-preserving case,
	// so fix the exact transformed token once after normalization.
	raw = strings.ReplaceAll(raw, ".22", ".2")
	raw = strings.ReplaceAll(raw, ".44", ".4")
	raw = strings.ReplaceAll(raw, ".11", ".1")
	return strings.TrimSpace(raw), continuation
}

// TokenPrices returns per-1k token prices for the specified model ID when
// available in the model configuration. Returns ok=false when no config exists
// or prices are not set.
func (d *Finder) TokenPrices(id string) (in float64, out float64, cached float64, ok bool) {
	if strings.TrimSpace(id) == "" {
		return 0, 0, 0, false
	}
	// 1) Try direct lookup by config id (registry key)
	if cfg, err := d.configRegistry.Lookup(context.Background(), id); err == nil && cfg != nil {
		in = cfg.Options.InputTokenPrice
		out = cfg.Options.OutputTokenPrice
		cached = cfg.Options.CachedTokenPrice
		if in != 0 || out != 0 || cached != 0 {
			return in, out, cached, true
		}
	}
	// 2) Fallback: scan all configs and match either config.ID or provider model name
	if all, err := d.configRegistry.List(context.Background()); err == nil {
		for _, cfg := range all {
			if cfg == nil {
				continue
			}
			if strings.EqualFold(cfg.ID, id) || strings.EqualFold(strings.TrimSpace(cfg.Options.Model), strings.TrimSpace(id)) {
				in = cfg.Options.InputTokenPrice
				out = cfg.Options.OutputTokenPrice
				cached = cfg.Options.CachedTokenPrice
				if in != 0 || out != 0 || cached != 0 {
					return in, out, cached, true
				}
			}
		}
	}
	return 0, 0, 0, false
}

// Candidates returns the matcher view of registered models. Cached by
// registry version — rebuilds only when configs change (provider added,
// removed, replaced). On cache hit returns the same slice every time;
// callers MUST treat the result as read-only (no mutation, no in-place
// sort). The matcher does so already.
//
// Concurrency: a single read lock + one fast atomic load on the hot
// path. Rebuild path takes a write lock, computes once, stores, then
// downgrades to read-only access for subsequent calls.
func (d *Finder) Candidates() []matcher.Candidate {
	currentVersion := atomic.LoadInt64(&d.version)
	// Fast path: cache hit.
	d.candCacheMux.RLock()
	if d.candCacheValid && d.candCacheVersion == currentVersion {
		out := d.candCache
		d.candCacheMux.RUnlock()
		return out
	}
	d.candCacheMux.RUnlock()
	return d.rebuildCandidates(currentVersion)
}

// rebuildCandidates computes the candidate slice once for the given
// registry version and stores it. If the registry version has advanced
// since `seen` was sampled, the rebuild still proceeds but stores the
// freshest version it sees so subsequent callers cache-hit.
func (d *Finder) rebuildCandidates(seen int64) []matcher.Candidate {
	configs, err := d.configRegistry.List(context.Background())
	if err != nil {
		return nil
	}
	out := make([]matcher.Candidate, 0, len(configs))
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		cost := 0.0
		if cfg.Options.InputTokenPrice > 0 || cfg.Options.OutputTokenPrice > 0 {
			cost = cfg.Options.InputTokenPrice + cfg.Options.OutputTokenPrice
		}
		base, ver := deriveBaseAndVersion(cfg.ID, cfg.Options.Model)
		out = append(out, matcher.Candidate{
			ID:           cfg.ID,
			Intelligence: cfg.Intelligence,
			Speed:        cfg.Speed,
			Cost:         cost,
			BaseModel:    base,
			Version:      ver,
		})
	}
	// Re-sample version under the write lock so we don't store a cache
	// keyed to a stale version number if the registry updated mid-build.
	current := atomic.LoadInt64(&d.version)
	d.candCacheMux.Lock()
	d.candCache = out
	d.candCacheVersion = current
	d.candCacheValid = true
	d.candCacheMux.Unlock()
	_ = seen // retained for telemetry/debugging if needed
	return out
}

// invalidateCandidatesCache forces the next Candidates() call to rebuild.
// Currently the version-keyed cache invalidates automatically, but this
// helper is kept as an explicit lever for tests and unusual reload paths
// that bump version state without going through the registry's normal
// mutation surface.
func (d *Finder) invalidateCandidatesCache() {
	d.candCacheMux.Lock()
	d.candCacheValid = false
	d.candCacheMux.Unlock()
}

func deriveBaseAndVersion(id, model string) (string, string) {
	src := strings.TrimSpace(model)
	if src == "" {
		src = strings.TrimSpace(id)
	}
	if src == "" {
		return "", ""
	}
	if idx := strings.IndexByte(src, '_'); idx > 0 {
		src = strings.TrimSpace(src[idx+1:])
	}
	if src == "" {
		return "", ""
	}
	base := src
	version := ""
	if i := strings.LastIndexByte(src, '-'); i > 0 && i+1 < len(src) {
		cand := strings.TrimSpace(src[i+1:])
		if isVersionToken(cand) {
			version = cand
			base = strings.TrimSpace(src[:i])
		}
	}
	return base, version
}

func isVersionToken(v string) bool {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" {
		return false
	}
	if len(v) == len("2006-01-02") && strings.Count(v, "-") == 2 {
		if _, err := time.Parse("2006-01-02", v); err == nil {
			return true
		}
	}
	for _, part := range strings.Split(v, ".") {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// Matcher builds a matcher instance from current configs.
func (d *Finder) Matcher() *matcher.Matcher {
	return matcher.New(d.Candidates())
}

func New(options ...Option) *Finder {
	dao := &Finder{
		modelFactory:   provider.New(),
		configRegistry: registry.New[*provider.Config](),
		models:         map[string]llm.Model{},
	}
	for _, option := range options {
		option(dao)
	}

	return dao
}

// Remove deletes a model configuration and any instantiated model from the
// finder caches. It bumps the internal version so hot-swap watchers can
// detect the change.
func (d *Finder) Remove(name string) {
	d.mux.Lock()
	delete(d.models, name)
	d.mux.Unlock()

	d.configRegistry.Remove(name)
	atomic.AddInt64(&d.version, 1)
}

// Version returns monotonically increasing value changed on Add/Remove.
func (d *Finder) Version() int64 { return atomic.LoadInt64(&d.version) }

// DropModel removes an already instantiated llm.Model instance but keeps its
// configuration. Next Find() will create a fresh model using the existing
// config. Useful after model implementation reload without deleting YAML.
func (d *Finder) DropModel(name string) {
	d.mux.Lock()
	if _, ok := d.models[name]; ok {
		delete(d.models, name)
		atomic.AddInt64(&d.version, 1)
	}
	d.mux.Unlock()
}

// AddConfig injects or overwrites a model configuration and bumps version.
func (d *Finder) AddConfig(name string, cfg *provider.Config) {
	if cfg == nil || name == "" {
		return
	}
	d.configRegistry.Add(name, cfg)
	// Drop any instantiated model to ensure next Find builds a fresh one.
	d.DropModel(name)
	atomic.AddInt64(&d.version, 1)
}
