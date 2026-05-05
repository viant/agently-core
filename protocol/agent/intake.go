package agent

import (
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

// Intake configures the pre-turn intake sidecar for an agent.
// The sidecar runs a lightweight LLM call before the main turn to extract
// structured metadata (title, intent, context) and optionally suggest a
// prompt profile, extra tool bundles, and an output template.
type Intake struct {
	// Enabled turns the intake sidecar on for this agent. Default: false.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// Prompt appends workspace-specific guidance to the shared intake classifier
	// prompt. Use this to tune routing/clarification behavior for a workspace
	// without forking the generic classifier instructions.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`

	// Scope lists which TurnContext fields the sidecar is allowed to populate.
	// Class A fields (safe for any agent): title, context, intent, clarification.
	// Class B fields (orchestrators only, opt-in): profile, tools, template.
	// When empty, defaults to Class A only.
	Scope []string `yaml:"scope,omitempty" json:"scope,omitempty"`

	// Model is the sidecar model id (e.g. "haiku"). Falls back to ModelPreferences
	// resolution and then to the tool-auto-selection model when empty.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`

	// ModelPreferences expresses MCP-style sampling preferences for selecting the
	// sidecar model. When Model is empty and ModelPreferences is non-nil, the
	// runtime resolves a concrete model via the existing
	// internal/finder/model.Finder. Both YAML hint shapes
	// (hints: ["X"] and hints: [{name: "X"}]) parse correctly via the custom
	// (Un)Marshal methods on llm.ModelPreferences.
	ModelPreferences *llm.ModelPreferences `yaml:"modelPreferences,omitempty" json:"modelPreferences,omitempty"`

	// MaxTokens caps the sidecar output. Default: 400.
	MaxTokens int `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`

	// ConfidenceThreshold is the minimum confidence score (0–1) required to
	// auto-populate PromptProfileId in the turn context. Default: 0.85.
	ConfidenceThreshold float64 `yaml:"confidenceThreshold,omitempty" json:"confidenceThreshold,omitempty"`

	// TriggerOnTopicShift re-runs the sidecar when the topic diverges from the
	// established conversation topic. Default: false.
	TriggerOnTopicShift bool `yaml:"triggerOnTopicShift,omitempty" json:"triggerOnTopicShift,omitempty"`

	// TopicShiftThreshold is the minimum divergence score (0–1) to trigger a
	// re-run when TriggerOnTopicShift is true. Default: 0.65.
	TopicShiftThreshold float64 `yaml:"topicShiftThreshold,omitempty" json:"topicShiftThreshold,omitempty"`

	// TimeoutSec caps the sidecar call. Default: 15.
	TimeoutSec int `yaml:"timeoutSec,omitempty" json:"timeoutSec,omitempty"`
}

// Intake scope constants.
const (
	// Class A — safe for any agent.
	IntakeScopeTitle   = "title"
	IntakeScopeContext = "context"
	// IntakeScopeEntities is retained as a backward-compatible alias for older
	// intake configs that still use "entities".
	IntakeScopeEntities      = "entities"
	IntakeScopeIntent        = "intent"
	IntakeScopeClarification = "clarification"

	// Class B — orchestrators only, opt-in.
	IntakeScopeProfile  = "profile"
	IntakeScopeTools    = "tools"
	IntakeScopeTemplate = "template"
)

// HasScope reports whether s is present in the scope list (case-insensitive).
func (in *Intake) HasScope(s string) bool {
	if in == nil {
		return false
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == IntakeScopeEntities {
		s = IntakeScopeContext
	}
	for _, v := range in.Scope {
		normalized := strings.ToLower(strings.TrimSpace(v))
		if normalized == IntakeScopeEntities {
			normalized = IntakeScopeContext
		}
		if normalized == s {
			return true
		}
	}
	return false
}

// EffectiveConfidenceThreshold returns the configured threshold or 0.85.
func (in *Intake) EffectiveConfidenceThreshold() float64 {
	if in == nil || in.ConfidenceThreshold <= 0 {
		return 0.85
	}
	return in.ConfidenceThreshold
}

// EffectiveTimeoutSec returns the configured timeout or 15.
func (in *Intake) EffectiveTimeoutSec() int {
	if in == nil || in.TimeoutSec <= 0 {
		return 15
	}
	return in.TimeoutSec
}

// EffectiveMaxTokens returns the configured max tokens or 400.
func (in *Intake) EffectiveMaxTokens() int {
	if in == nil || in.MaxTokens <= 0 {
		return 400
	}
	return in.MaxTokens
}
