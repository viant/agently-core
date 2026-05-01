package skill

import (
	"strconv"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

type Frontmatter struct {
	Name         string           `yaml:"name"`
	Description  string           `yaml:"description"`
	License      string           `yaml:"license,omitempty"`
	Metadata     map[string]any   `yaml:"metadata,omitempty"`
	Agently      *AgentlyMetadata `yaml:"-"`
	AllowedTools string           `yaml:"allowed-tools,omitempty"`
	Raw          map[string]any   `yaml:"-"`
}

type AgentlyMetadata struct {
	Context              string
	AgentID              string
	Model                string
	Effort               string
	Temperature          *float64
	MaxTokens            *int
	Preprocess           *bool
	PreprocessTimeoutSec *int
	AsyncNarratorPrompt  string
	ModelPreferences     *llm.ModelPreferences
}

type Skill struct {
	Frontmatter Frontmatter
	Body        string
	Root        string
	Path        string
	Source      string
}

type Metadata struct {
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	ExecutionMode string `json:"executionMode,omitempty"`
}

type Diagnostic struct {
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
	Path    string `json:"path,omitempty"`
}

// NormalizeContextMode resolves a skill's `context:` value to one of the
// canonical execution modes:
//
//	"inline"  — body injected into the current turn (default)
//	"fork"    — child agent in its own conversation; runtime awaits result
//	"detach"  — child agent in its own conversation; fire-and-forget
//
// Unset or unrecognized values default to "inline" — the safest cross-runtime
// behavior, matching how Claude / Codex parsers treat unknown execution-mode
// hints (the body just runs in the current context). Authors who want
// fork/detach must opt in explicitly via metadata.agently-context.
func NormalizeContextMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "inline"
	case "inline":
		return "inline"
	case "fork", "detach":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "inline"
	}
}

// ContextMode returns the canonical execution-mode hint (inline / fork /
// detach). Legacy top-level fields are normalized in parser.go; runtime code
// only reads canonical Agently metadata from Frontmatter.
func (f Frontmatter) ContextMode() string {
	if f.Agently != nil && strings.TrimSpace(f.Agently.Context) != "" {
		return NormalizeContextMode(f.Agently.Context)
	}
	return NormalizeContextMode("")
}

// AgentIDValue returns the skill's canonical preferred child-agent identity
// for fork/detach runs. Legacy top-level fields are normalized in parser.go.
func (f Frontmatter) AgentIDValue() string {
	if f.Agently != nil && strings.TrimSpace(f.Agently.AgentID) != "" {
		return strings.TrimSpace(f.Agently.AgentID)
	}
	return ""
}

// ModelValue returns the canonical exact-name model override for this skill.
func (f Frontmatter) ModelValue() string {
	if f.Agently != nil && strings.TrimSpace(f.Agently.Model) != "" {
		return strings.TrimSpace(f.Agently.Model)
	}
	return ""
}

// EffortValue returns the canonical reasoning-effort hint.
func (f Frontmatter) EffortValue() string {
	if f.Agently != nil && strings.TrimSpace(f.Agently.Effort) != "" {
		return strings.TrimSpace(f.Agently.Effort)
	}
	return ""
}

// TemperatureValue returns the canonical per-skill sampling temperature.
func (f Frontmatter) TemperatureValue() *float64 {
	if f.Agently != nil && f.Agently.Temperature != nil {
		return f.Agently.Temperature
	}
	return nil
}

// MaxTokensValue returns the canonical per-skill max-output-tokens cap.
func (f Frontmatter) MaxTokensValue() int {
	if f.Agently != nil && f.Agently.MaxTokens != nil {
		return *f.Agently.MaxTokens
	}
	return 0
}

// PreprocessEnabled reports the canonical preprocess flag.
func (f Frontmatter) PreprocessEnabled() bool {
	if f.Agently != nil && f.Agently.Preprocess != nil {
		return *f.Agently.Preprocess
	}
	return false
}

// PreprocessTimeoutValue returns the canonical preprocess timeout.
func (f Frontmatter) PreprocessTimeoutValue() int {
	if f.Agently != nil && f.Agently.PreprocessTimeoutSec != nil {
		return *f.Agently.PreprocessTimeoutSec
	}
	return 0
}

// AsyncNarratorPromptValue returns the canonical async narrator override.
func (f Frontmatter) AsyncNarratorPromptValue() string {
	if f.Agently != nil && strings.TrimSpace(f.Agently.AsyncNarratorPrompt) != "" {
		return strings.TrimSpace(f.Agently.AsyncNarratorPrompt)
	}
	return ""
}

// ModelPreferencesValue returns the canonical MCP-aligned model preferences.
func (f Frontmatter) ModelPreferencesValue() *llm.ModelPreferences {
	if f.Agently != nil && f.Agently.ModelPreferences != nil {
		return f.Agently.ModelPreferences
	}
	return nil
}

type LegacyAgentlyFields struct {
	Context              string
	AgentID              string
	Model                string
	Effort               string
	Temperature          *float64
	MaxTokens            *int
	Preprocess           *bool
	PreprocessTimeoutSec *int
	AsyncNarratorPrompt  string
}

func parseAgentlyMetadata(metadata map[string]any, legacy *LegacyAgentlyFields) *AgentlyMetadata {
	if len(metadata) == 0 && legacy == nil {
		return nil
	}
	ret := &AgentlyMetadata{}
	source := metadata
	if nested, ok := metadata["agently"].(map[string]any); ok && len(nested) > 0 {
		source = nested
	}
	if value, ok := metadataStringValue(source, "context"); ok && strings.TrimSpace(value) != "" {
		ret.Context = strings.TrimSpace(value)
	} else if value, ok := metadataStringValue(metadata, "agently-context"); ok && strings.TrimSpace(value) != "" {
		ret.Context = strings.TrimSpace(value)
	}
	if value, ok := metadataStringValue(source, "agent-id"); ok && strings.TrimSpace(value) != "" {
		ret.AgentID = strings.TrimSpace(value)
	} else if value, ok := metadataStringValue(metadata, "agently-agent-id"); ok && strings.TrimSpace(value) != "" {
		ret.AgentID = strings.TrimSpace(value)
	}
	if value, ok := metadataStringValue(source, "model"); ok && strings.TrimSpace(value) != "" {
		ret.Model = strings.TrimSpace(value)
	} else if value, ok := metadataStringValue(metadata, "agently-model"); ok && strings.TrimSpace(value) != "" {
		ret.Model = strings.TrimSpace(value)
	}
	if value, ok := metadataStringValue(source, "effort"); ok && strings.TrimSpace(value) != "" {
		ret.Effort = strings.TrimSpace(value)
	} else if value, ok := metadataStringValue(metadata, "agently-effort"); ok && strings.TrimSpace(value) != "" {
		ret.Effort = strings.TrimSpace(value)
	}
	if value, ok := metadataFloatValue(source, "temperature"); ok {
		ret.Temperature = &value
	} else if value, ok := metadataFloatValue(metadata, "agently-temperature"); ok {
		ret.Temperature = &value
	}
	if value, ok := metadataIntValue(source, "max-tokens"); ok {
		ret.MaxTokens = &value
	} else if value, ok := metadataIntValue(metadata, "agently-max-tokens"); ok {
		ret.MaxTokens = &value
	}
	if value, ok := metadataBoolValue(source, "preprocess"); ok {
		ret.Preprocess = &value
	} else if value, ok := metadataBoolValue(metadata, "agently-preprocess"); ok {
		ret.Preprocess = &value
	}
	if value, ok := metadataIntValue(source, "preprocess-timeout"); ok {
		ret.PreprocessTimeoutSec = &value
	} else if value, ok := metadataIntValue(metadata, "agently-preprocess-timeout"); ok {
		ret.PreprocessTimeoutSec = &value
	}
	if value, ok := metadataStringValue(source, "async-narrator-prompt"); ok && strings.TrimSpace(value) != "" {
		ret.AsyncNarratorPrompt = strings.TrimSpace(value)
	} else if value, ok := metadataStringValue(metadata, "agently-async-narrator-prompt"); ok && strings.TrimSpace(value) != "" {
		ret.AsyncNarratorPrompt = strings.TrimSpace(value)
	}
	if prefs := parseModelPreferences(source); prefs != nil {
		ret.ModelPreferences = prefs
	} else if prefs := parseModelPreferences(metadata); prefs != nil {
		ret.ModelPreferences = prefs
	}
	if strings.TrimSpace(ret.Context) == "" && legacy != nil {
		ret.Context = strings.TrimSpace(legacy.Context)
	}
	if strings.TrimSpace(ret.AgentID) == "" && legacy != nil {
		ret.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	if strings.TrimSpace(ret.Model) == "" && legacy != nil {
		ret.Model = strings.TrimSpace(legacy.Model)
	}
	if strings.TrimSpace(ret.Effort) == "" && legacy != nil {
		ret.Effort = strings.TrimSpace(legacy.Effort)
	}
	if ret.Temperature == nil && legacy != nil {
		ret.Temperature = legacy.Temperature
	}
	if ret.MaxTokens == nil && legacy != nil {
		ret.MaxTokens = legacy.MaxTokens
	}
	if ret.Preprocess == nil && legacy != nil {
		ret.Preprocess = legacy.Preprocess
	}
	if ret.PreprocessTimeoutSec == nil && legacy != nil {
		ret.PreprocessTimeoutSec = legacy.PreprocessTimeoutSec
	}
	if strings.TrimSpace(ret.AsyncNarratorPrompt) == "" && legacy != nil {
		ret.AsyncNarratorPrompt = strings.TrimSpace(legacy.AsyncNarratorPrompt)
	}
	if ret.Context == "" && ret.AgentID == "" && ret.Model == "" && ret.Effort == "" &&
		ret.Temperature == nil && ret.MaxTokens == nil && ret.Preprocess == nil &&
		ret.PreprocessTimeoutSec == nil && ret.AsyncNarratorPrompt == "" && ret.ModelPreferences == nil {
		return nil
	}
	return ret
}

func parseModelPreferences(values map[string]any) *llm.ModelPreferences {
	if len(values) == 0 {
		return nil
	}
	raw, ok := values["model-preferences"]
	if !ok || raw == nil {
		return nil
	}
	obj, ok := raw.(map[string]any)
	if !ok || len(obj) == 0 {
		return nil
	}
	prefs := &llm.ModelPreferences{}
	if value, ok := metadataFloatValue(obj, "intelligencePriority"); ok {
		prefs.IntelligencePriority = value
	}
	if value, ok := metadataFloatValue(obj, "speedPriority"); ok {
		prefs.SpeedPriority = value
	}
	if value, ok := metadataFloatValue(obj, "costPriority"); ok {
		prefs.CostPriority = value
	}
	if hints, ok := obj["hints"]; ok && hints != nil {
		switch actual := hints.(type) {
		case []any:
			for _, item := range actual {
				switch hint := item.(type) {
				case map[string]any:
					if name, ok := metadataStringValue(hint, "name"); ok && strings.TrimSpace(name) != "" {
						prefs.Hints = append(prefs.Hints, strings.TrimSpace(name))
					}
				case string:
					if text := strings.TrimSpace(hint); text != "" {
						prefs.Hints = append(prefs.Hints, text)
					}
				}
			}
		case []string:
			for _, hint := range actual {
				if text := strings.TrimSpace(hint); text != "" {
					prefs.Hints = append(prefs.Hints, text)
				}
			}
		}
	}
	if prefs.IntelligencePriority == 0 && prefs.SpeedPriority == 0 && prefs.CostPriority == 0 && len(prefs.Hints) == 0 {
		return nil
	}
	return prefs
}

func (f Frontmatter) metadataString(key string) (string, bool) {
	if len(f.Metadata) == 0 {
		return "", false
	}
	return metadataStringValue(f.Metadata, key)
}

func metadataStringValue(values map[string]any, key string) (string, bool) {
	if len(values) == 0 {
		return "", false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return "", false
	}
	switch actual := raw.(type) {
	case string:
		return strings.TrimSpace(actual), true
	case []byte:
		return strings.TrimSpace(string(actual)), true
	default:
		return strings.TrimSpace(strings.TrimSpace(toString(raw))), strings.TrimSpace(toString(raw)) != ""
	}
}

func (f Frontmatter) metadataBool(key string) (bool, bool) {
	if len(f.Metadata) == 0 {
		return false, false
	}
	return metadataBoolValue(f.Metadata, key)
}

func metadataBoolValue(values map[string]any, key string) (bool, bool) {
	if len(values) == 0 {
		return false, false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return false, false
	}
	switch actual := raw.(type) {
	case bool:
		return actual, true
	case string:
		v, err := strconv.ParseBool(strings.TrimSpace(actual))
		return v, err == nil
	default:
		return false, false
	}
}

func (f Frontmatter) metadataInt(key string) (int, bool) {
	if len(f.Metadata) == 0 {
		return 0, false
	}
	return metadataIntValue(f.Metadata, key)
}

func metadataIntValue(values map[string]any, key string) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch actual := raw.(type) {
	case int:
		return actual, true
	case int64:
		return int(actual), true
	case float64:
		return int(actual), true
	case string:
		v, err := strconv.Atoi(strings.TrimSpace(actual))
		return v, err == nil
	default:
		return 0, false
	}
}

func (f Frontmatter) metadataFloat(key string) (float64, bool) {
	if len(f.Metadata) == 0 {
		return 0, false
	}
	return metadataFloatValue(f.Metadata, key)
}

func metadataFloatValue(values map[string]any, key string) (float64, bool) {
	if len(values) == 0 {
		return 0, false
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return 0, false
	}
	switch actual := raw.(type) {
	case float64:
		return actual, true
	case float32:
		return float64(actual), true
	case int:
		return float64(actual), true
	case int64:
		return float64(actual), true
	case string:
		v, err := strconv.ParseFloat(strings.TrimSpace(actual), 64)
		return v, err == nil
	default:
		return 0, false
	}
}

func toString(value any) string {
	switch actual := value.(type) {
	case string:
		return actual
	case []byte:
		return string(actual)
	case int:
		return strconv.Itoa(actual)
	case int64:
		return strconv.FormatInt(actual, 10)
	case float64:
		return strconv.FormatFloat(actual, 'f', -1, 64)
	case bool:
		if actual {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}
