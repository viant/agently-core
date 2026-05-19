package agent

import (
	"encoding/json"
	"strings"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"gopkg.in/yaml.v3"
)

// IntakePrompt supports both the legacy scalar-string intake prompt and the
// richer prompt object shape (`text` / `uri` / `engine`).
type IntakePrompt struct {
	binding.Prompt `yaml:",inline" json:",inline"`
}

func (p *IntakePrompt) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		*p = IntakePrompt{}
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		p.Text = node.Value
		p.URI = ""
		p.Engine = ""
		return nil
	case yaml.MappingNode:
		var prompt binding.Prompt
		if err := node.Decode(&prompt); err != nil {
			return err
		}
		p.Prompt = prompt
		return nil
	default:
		*p = IntakePrompt{}
		return nil
	}
}

func (p IntakePrompt) MarshalYAML() (interface{}, error) {
	if strings.TrimSpace(p.URI) == "" && strings.TrimSpace(p.Engine) == "" {
		return p.Text, nil
	}
	return p.Prompt, nil
}

func (p *IntakePrompt) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*p = IntakePrompt{}
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		p.Text = text
		p.URI = ""
		p.Engine = ""
		return nil
	}
	var prompt binding.Prompt
	if err := json.Unmarshal(data, &prompt); err != nil {
		return err
	}
	p.Prompt = prompt
	return nil
}

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
	Prompt IntakePrompt `yaml:"prompt,omitempty" json:"prompt,omitempty"`

	// ActivationRules declare deterministic workspace-owned intake actions for
	// exact operational asks. Core only evaluates the generic matching and
	// substitution contract; the actual phrases, parameters, and assistant text
	// remain workspace-defined.
	ActivationRules []ActivationRule `yaml:"activationRules,omitempty" json:"activationRules,omitempty"`

	// Tool reuses the agent Tool shape to authorize deterministic directAction
	// execution proposed by intake. This keeps direct-action policy on the same
	// items/bundles abstraction as the normal agent tool surface instead of a
	// separate hardcoded allowlist.
	Tool Tool `yaml:"tool,omitempty" json:"tool,omitempty"`

	// Scope lists which intake Context fields the sidecar is allowed to populate.
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

	// MaxTokens caps the sidecar output. Default: 800.
	MaxTokens int `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`

	// ConfidenceThreshold is the minimum confidence score (0–1) required to
	// auto-populate PromptProfileId in the turn context. Default: 0.85.
	ConfidenceThreshold float64 `yaml:"confidenceThreshold,omitempty" json:"confidenceThreshold,omitempty"`

	// PlannerEnabled allows the workspace router / planner path to activate
	// planner mode for turns targeting this agent. Default: false.
	PlannerEnabled bool `yaml:"plannerEnabled,omitempty" json:"plannerEnabled,omitempty"`

	// PlannerAgentID optionally selects a dedicated planner agent to run the
	// planning pass for turns targeting this execution agent. When empty, the
	// runtime falls back to the selected execution agent's own prompts/model for
	// planning, preserving current behavior.
	PlannerAgentID string `yaml:"plannerAgentId,omitempty" json:"plannerAgentId,omitempty"`

	// PlannerFallbackThreshold reserves a router-side threshold knob for future
	// planner-specific confidence use. Default: 0.70.
	PlannerFallbackThreshold float64 `yaml:"plannerFallbackThreshold,omitempty" json:"plannerFallbackThreshold,omitempty"`

	// PlannerOnValidatorFailure enables the post-routing validator failure
	// planner path. Wired in planner phase 2.
	PlannerOnValidatorFailure bool `yaml:"plannerOnValidatorFailure,omitempty" json:"plannerOnValidatorFailure,omitempty"`

	// PlannerOnCreativeRequest allows the router prompt to select planner mode
	// for explicit creative/exploratory asks. Runtime orchestration must not
	// infer planner mode from wording alone; planner mode is explicit intake
	// output. Default: false.
	PlannerOnCreativeRequest bool `yaml:"plannerOnCreativeRequest,omitempty" json:"plannerOnCreativeRequest,omitempty"`

	// PlannerSecondFailurePolicy controls the planner retry terminal path.
	// Supported values: "clarify" | "block". Default: "clarify".
	PlannerSecondFailurePolicy string `yaml:"plannerSecondFailurePolicy,omitempty" json:"plannerSecondFailurePolicy,omitempty"`

	// PlannerTriggerPhrases reserves explicit phrase hooks for future router
	// prompt expansion. Not wired in phase 1.
	PlannerTriggerPhrases []string `yaml:"plannerTriggerPhrases,omitempty" json:"plannerTriggerPhrases,omitempty"`

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

// EffectivePlannerFallbackThreshold returns the configured threshold or 0.70.
func (in *Intake) EffectivePlannerFallbackThreshold() float64 {
	if in == nil || in.PlannerFallbackThreshold <= 0 {
		return 0.70
	}
	return in.PlannerFallbackThreshold
}

// EffectiveTimeoutSec returns the configured timeout or 15.
func (in *Intake) EffectiveTimeoutSec() int {
	if in == nil || in.TimeoutSec <= 0 {
		return 15
	}
	return in.TimeoutSec
}

// EffectiveMaxTokens returns the configured max tokens or 800.
func (in *Intake) EffectiveMaxTokens() int {
	if in == nil || in.MaxTokens <= 0 {
		return 800
	}
	return in.MaxTokens
}

type ActivationRule struct {
	ID             string                   `yaml:"id,omitempty" json:"id,omitempty"`
	Mode           string                   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Source         string                   `yaml:"source,omitempty" json:"source,omitempty"`
	WindowKey      string                   `yaml:"windowKey,omitempty" json:"windowKey,omitempty"`
	Match          ActivationMatch          `yaml:"match,omitempty" json:"match,omitempty"`
	Classification ActivationClassification `yaml:"classification,omitempty" json:"classification,omitempty"`
	Prompting      ActivationPrompting      `yaml:"prompting,omitempty" json:"prompting,omitempty"`
	Scope          ActivationScope          `yaml:"scope,omitempty" json:"scope,omitempty"`
	Action         ActivationAction         `yaml:"action,omitempty" json:"action,omitempty"`
	Response       ActivationResponse       `yaml:"response,omitempty" json:"response,omitempty"`
	SurfaceMatch   *ActivationSurfaceMatch  `yaml:"surfaceMatch,omitempty" json:"surfaceMatch,omitempty"`
}

type ActivationMatch struct {
	Pattern    string                         `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	Patterns   []string                       `yaml:"patterns,omitempty" json:"patterns,omitempty"`
	Flags      string                         `yaml:"flags,omitempty" json:"flags,omitempty"`
	Extractors map[string]ActivationExtractor `yaml:"extractors,omitempty" json:"extractors,omitempty"`
}

type ActivationExtractor struct {
	Type    string `yaml:"type,omitempty" json:"type,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"`
}

type ActivationClassification struct {
	Intent     string  `yaml:"intent,omitempty" json:"intent,omitempty"`
	Confidence float64 `yaml:"confidence,omitempty" json:"confidence,omitempty"`
}

type ActivationPrompting struct {
	SuggestedProfileID string `yaml:"suggestedProfileId,omitempty" json:"suggestedProfileId,omitempty"`
	TemplateID         string `yaml:"templateId,omitempty" json:"templateId,omitempty"`
}

type ActivationScope struct {
	Values map[string]string `yaml:"values,omitempty" json:"values,omitempty"`
}

type ActivationAction struct {
	Tool    string                 `yaml:"tool,omitempty" json:"tool,omitempty"`
	Input   map[string]interface{} `yaml:"input,omitempty" json:"input,omitempty"`
	Foreach string                 `yaml:"foreach,omitempty" json:"foreach,omitempty"`
	Item    map[string]interface{} `yaml:"item,omitempty" json:"item,omitempty"`
}

type ActivationResponse struct {
	AssistantText string `yaml:"assistantText,omitempty" json:"assistantText,omitempty"`
}

type ActivationSurfaceMatch struct {
	Tabs           []string                                 `yaml:"tabs,omitempty" json:"tabs,omitempty"`
	Controls       []string                                 `yaml:"controls,omitempty" json:"controls,omitempty"`
	TabAliases     map[string]string                        `yaml:"tabAliases,omitempty" json:"tabAliases,omitempty"`
	ControlAliases map[string]ActivationSurfaceControlAlias `yaml:"controlAliases,omitempty" json:"controlAliases,omitempty"`
}

type ActivationSurfaceControlAlias struct {
	ControlID  string      `yaml:"controlId,omitempty" json:"controlId,omitempty"`
	Value      interface{} `yaml:"value,omitempty" json:"value,omitempty"`
	ValueLabel string      `yaml:"valueLabel,omitempty" json:"valueLabel,omitempty"`
}
