package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/viant/mcp-protocol/schema"
	"gopkg.in/yaml.v3"
)

type ModelSelection struct {
	Model       string            `yaml:"model,omitempty" json:"model,omitempty"`
	Preferences *ModelPreferences `yaml:"modelPreferences,omitempty" json:"modelPreferences,omitempty"`
	Options     *Options          `yaml:"options,omitempty" json:"options,omitempty"`
	// Internal-only filters for model selection gatekeeping. These are set by
	// agent runtime from agent configuration and are not part of public JSON.
	AllowedProviders []string `yaml:"-" json:"-"`
	AllowedModels    []string `yaml:"-" json:"-"`
}

// ModelPreferences expresses caller priorities (0..1) + optional name hints.
//
// The Hints field is the canonical in-memory shape ([]string), but YAML/JSON
// inputs may use either the simple string-list form or the MCP object form:
//
//	hints: [claude-haiku, gpt-5-mini]
//	hints: [{name: claude-haiku}, {name: gpt-5-mini}]
//
// Both forms (and mixed-form lists) parse identically into Hints []string via
// the custom UnmarshalYAML / UnmarshalJSON methods below. Authors can copy
// MCP-spec examples or write the simpler form interchangeably.
type ModelPreferences struct {
	IntelligencePriority float64  `yaml:"intelligencePriority,omitempty" json:"intelligencePriority,omitempty"`
	SpeedPriority        float64  `yaml:"speedPriority,omitempty" json:"speedPriority,omitempty"`
	CostPriority         float64  `yaml:"costPriority,omitempty" json:"costPriority,omitempty"`
	Hints                []string `yaml:"hints,omitempty" json:"hints,omitempty" description:"model name"`
}

// ModelPreferencesOption // is a functional option for ModelPreferences.
type ModelPreferencesOption func(*ModelPreferences)

func NewModelPreferences(options ...ModelPreferencesOption) *ModelPreferences {
	ret := &ModelPreferences{
		IntelligencePriority: 0.5,
		SpeedPriority:        0.5,
		CostPriority:         0.5,
		Hints:                make([]string, 0),
	}
	for _, opt := range options {
		opt(ret)
	}
	return ret
}

// FromEffort maps the deprecated `effort: low|medium|high` skill frontmatter
// hint to an equivalent *ModelPreferences. The legacy `effort` field is a
// reasoning-effort enum; the modern equivalent splits the same intent across
// the MCP-aligned IntelligencePriority and SpeedPriority knobs:
//
//	low    → IntelligencePriority=0.2, SpeedPriority=0.8 (cheap & fast)
//	medium → IntelligencePriority=0.5, SpeedPriority=0.5 (balanced)
//	high   → IntelligencePriority=0.9, SpeedPriority=0.2 (slow & smart)
//
// Returns nil for unrecognized values. Skill parsers invoke this when they
// see the deprecated bare `effort:` key so the runtime resolves a model via
// the same Matcher path used by `metadata.model-preferences`.
//
// Authors should migrate to `metadata.model-preferences` directly. The
// parser emits a warn-level diagnostic when the legacy form is used.
func FromEffort(effort string) *ModelPreferences {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return &ModelPreferences{IntelligencePriority: 0.2, SpeedPriority: 0.8}
	case "medium", "med":
		return &ModelPreferences{IntelligencePriority: 0.5, SpeedPriority: 0.5}
	case "high":
		return &ModelPreferences{IntelligencePriority: 0.9, SpeedPriority: 0.2}
	}
	return nil
}

func WithPreferences(preferences *schema.ModelPreferences) ModelPreferencesOption {
	return func(p *ModelPreferences) {
		if preferences.IntelligencePriority != nil {
			p.IntelligencePriority = *preferences.IntelligencePriority
		}
		if preferences.SpeedPriority != nil {
			p.SpeedPriority = *preferences.SpeedPriority
		}
		if preferences.CostPriority != nil {
			p.CostPriority = *preferences.CostPriority
		}
		for _, hint := range preferences.Hints {
			if hint.Name != nil {
				p.Hints = append(p.Hints, *hint.Name)
			}
		}
	}
}

// UnmarshalYAML accepts both YAML shapes for hints and normalizes to []string.
//
//	hints: [claude-haiku]                      -> ["claude-haiku"]
//	hints: [{name: claude-haiku}]              -> ["claude-haiku"]
//	hints: [claude-haiku, {name: gpt-5-mini}]  -> ["claude-haiku", "gpt-5-mini"]
//
// Empty / whitespace-only entries are dropped silently. Mixed shapes within
// the same list are tolerated.
func (p *ModelPreferences) UnmarshalYAML(node *yaml.Node) error {
	if p == nil {
		return errors.New("ModelPreferences: cannot unmarshal into nil receiver")
	}
	type rawPriorities struct {
		IntelligencePriority float64     `yaml:"intelligencePriority,omitempty"`
		SpeedPriority        float64     `yaml:"speedPriority,omitempty"`
		CostPriority         float64     `yaml:"costPriority,omitempty"`
		Hints                []yaml.Node `yaml:"hints,omitempty"`
	}
	var r rawPriorities
	if err := node.Decode(&r); err != nil {
		return err
	}
	p.IntelligencePriority = r.IntelligencePriority
	p.SpeedPriority = r.SpeedPriority
	p.CostPriority = r.CostPriority
	p.Hints = nil
	for _, hn := range r.Hints {
		// Resolve alias to its target so the kind switch sees the underlying node.
		target := &hn
		if target.Kind == yaml.AliasNode && target.Alias != nil {
			target = target.Alias
		}
		switch target.Kind {
		case yaml.ScalarNode:
			var s string
			if err := target.Decode(&s); err != nil {
				return fmt.Errorf("ModelPreferences.hints scalar: %w", err)
			}
			if s = strings.TrimSpace(s); s != "" {
				p.Hints = append(p.Hints, s)
			}
		case yaml.MappingNode:
			var obj struct {
				Name string `yaml:"name"`
			}
			if err := target.Decode(&obj); err != nil {
				return fmt.Errorf("ModelPreferences.hints mapping: %w", err)
			}
			if name := strings.TrimSpace(obj.Name); name != "" {
				p.Hints = append(p.Hints, name)
			}
		default:
			return fmt.Errorf("ModelPreferences.hints: unsupported YAML node kind %v", target.Kind)
		}
	}
	return nil
}

// UnmarshalJSON accepts the same two shapes as UnmarshalYAML:
//
//	{"hints": ["claude-haiku"]}
//	{"hints": [{"name": "claude-haiku"}]}
//	{"hints": ["claude-haiku", {"name": "gpt-5-mini"}]}
//
// All shapes normalize to Hints []string. Empty entries dropped.
func (p *ModelPreferences) UnmarshalJSON(data []byte) error {
	if p == nil {
		return errors.New("ModelPreferences: cannot unmarshal into nil receiver")
	}
	type rawPriorities struct {
		IntelligencePriority float64           `json:"intelligencePriority,omitempty"`
		SpeedPriority        float64           `json:"speedPriority,omitempty"`
		CostPriority         float64           `json:"costPriority,omitempty"`
		Hints                []json.RawMessage `json:"hints,omitempty"`
	}
	var r rawPriorities
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	p.IntelligencePriority = r.IntelligencePriority
	p.SpeedPriority = r.SpeedPriority
	p.CostPriority = r.CostPriority
	p.Hints = nil
	for _, h := range r.Hints {
		// Try string form first (most common).
		var s string
		if err := json.Unmarshal(h, &s); err == nil {
			if s = strings.TrimSpace(s); s != "" {
				p.Hints = append(p.Hints, s)
			}
			continue
		}
		// Fall back to {"name": "..."} object form.
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(h, &obj); err != nil {
			return fmt.Errorf("ModelPreferences.hints: %w", err)
		}
		if name := strings.TrimSpace(obj.Name); name != "" {
			p.Hints = append(p.Hints, name)
		}
	}
	return nil
}
