package skill

import (
	"strconv"
	"strings"

	"github.com/viant/agently-core/genai/llm"
)

type Frontmatter struct {
	Name                     string           `yaml:"name"`
	Description              string           `yaml:"description"`
	License                  string           `yaml:"license,omitempty"`
	Metadata                 map[string]any   `yaml:"metadata,omitempty"`
	Agently                  *AgentlyMetadata `yaml:"-"`
	Context                  string           `yaml:"context,omitempty"`
	AgentID                  string           `yaml:"agent-id,omitempty"`
	AllowedTools             string           `yaml:"allowed-tools,omitempty"`
	Model                    string           `yaml:"model,omitempty"`
	Effort                   string           `yaml:"effort,omitempty"`
	Temperature              *float64         `yaml:"temperature,omitempty"`
	MaxTokens                int              `yaml:"max-tokens,omitempty"`
	Preprocess               bool             `yaml:"preprocess,omitempty"`
	PreprocessTimeoutSeconds int              `yaml:"preprocess-timeout,omitempty"`
	// AsyncNarratorPrompt overrides the agent-level and workspace-level
	// async narrator system prompt when this skill is the active skill
	// for a turn. Resolution order (highest precedence first):
	// active-skill → agent → workspace default. Empty → fall through
	// to the next level.
	AsyncNarratorPrompt string         `yaml:"async-narrator-prompt,omitempty"`
	Raw                 map[string]any `yaml:"-"`
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
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type Diagnostic struct {
	Level   string `json:"level,omitempty"`
	Message string `json:"message,omitempty"`
	Path    string `json:"path,omitempty"`
}

func NormalizeContextMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "fork"
	case "inline":
		return "inline"
	case "fork", "detach":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "fork"
	}
}

func (f Frontmatter) ContextMode() string {
	if ag := f.agentlyMetadata(); ag != nil && strings.TrimSpace(ag.Context) != "" {
		return NormalizeContextMode(ag.Context)
	}
	return NormalizeContextMode("")
}

func (f Frontmatter) AgentIDValue() string {
	if ag := f.agentlyMetadata(); ag != nil && strings.TrimSpace(ag.AgentID) != "" {
		return strings.TrimSpace(ag.AgentID)
	}
	return ""
}

func (f Frontmatter) ModelValue() string {
	if ag := f.agentlyMetadata(); ag != nil && strings.TrimSpace(ag.Model) != "" {
		return strings.TrimSpace(ag.Model)
	}
	return ""
}

func (f Frontmatter) EffortValue() string {
	if ag := f.agentlyMetadata(); ag != nil && strings.TrimSpace(ag.Effort) != "" {
		return strings.TrimSpace(ag.Effort)
	}
	return ""
}

func (f Frontmatter) TemperatureValue() *float64 {
	if ag := f.agentlyMetadata(); ag != nil && ag.Temperature != nil {
		return ag.Temperature
	}
	return nil
}

func (f Frontmatter) MaxTokensValue() int {
	if ag := f.agentlyMetadata(); ag != nil && ag.MaxTokens != nil {
		return *ag.MaxTokens
	}
	return 0
}

func (f Frontmatter) PreprocessEnabled() bool {
	if ag := f.agentlyMetadata(); ag != nil && ag.Preprocess != nil {
		return *ag.Preprocess
	}
	return false
}

func (f Frontmatter) PreprocessTimeoutValue() int {
	if ag := f.agentlyMetadata(); ag != nil && ag.PreprocessTimeoutSec != nil {
		return *ag.PreprocessTimeoutSec
	}
	return 0
}

func (f Frontmatter) AsyncNarratorPromptValue() string {
	if ag := f.agentlyMetadata(); ag != nil && strings.TrimSpace(ag.AsyncNarratorPrompt) != "" {
		return strings.TrimSpace(ag.AsyncNarratorPrompt)
	}
	return ""
}

func (f Frontmatter) ModelPreferencesValue() *llm.ModelPreferences {
	if ag := f.agentlyMetadata(); ag != nil && ag.ModelPreferences != nil {
		return ag.ModelPreferences
	}
	return nil
}

func (f Frontmatter) agentlyMetadata() *AgentlyMetadata {
	if f.Agently != nil {
		return f.Agently
	}
	return parseAgentlyMetadata(f)
}

func parseAgentlyMetadata(f Frontmatter) *AgentlyMetadata {
	metadata := f.Metadata
	if len(metadata) == 0 {
		if strings.TrimSpace(f.Context) == "" && strings.TrimSpace(f.AgentID) == "" && strings.TrimSpace(f.Model) == "" &&
			strings.TrimSpace(f.Effort) == "" && f.Temperature == nil && f.MaxTokens == 0 &&
			!f.Preprocess && f.PreprocessTimeoutSeconds == 0 && strings.TrimSpace(f.AsyncNarratorPrompt) == "" {
			return nil
		}
		return legacyAgentlyMetadata(f)
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
	legacy := legacyAgentlyMetadata(f)
	if strings.TrimSpace(ret.Context) == "" && legacy != nil {
		ret.Context = legacy.Context
	}
	if strings.TrimSpace(ret.AgentID) == "" && legacy != nil {
		ret.AgentID = legacy.AgentID
	}
	if strings.TrimSpace(ret.Model) == "" && legacy != nil {
		ret.Model = legacy.Model
	}
	if strings.TrimSpace(ret.Effort) == "" && legacy != nil {
		ret.Effort = legacy.Effort
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
		ret.AsyncNarratorPrompt = legacy.AsyncNarratorPrompt
	}
	if ret.Context == "" && ret.AgentID == "" && ret.Model == "" && ret.Effort == "" &&
		ret.Temperature == nil && ret.MaxTokens == nil && ret.Preprocess == nil &&
		ret.PreprocessTimeoutSec == nil && ret.AsyncNarratorPrompt == "" && ret.ModelPreferences == nil {
		return nil
	}
	return ret
}

func legacyAgentlyMetadata(f Frontmatter) *AgentlyMetadata {
	ret := &AgentlyMetadata{}
	if text := strings.TrimSpace(f.Context); text != "" {
		ret.Context = text
	}
	if text := strings.TrimSpace(f.AgentID); text != "" {
		ret.AgentID = text
	}
	if text := strings.TrimSpace(f.Model); text != "" {
		ret.Model = text
	}
	if text := strings.TrimSpace(f.Effort); text != "" {
		ret.Effort = text
	}
	if f.Temperature != nil {
		ret.Temperature = f.Temperature
	}
	if f.MaxTokens > 0 {
		v := f.MaxTokens
		ret.MaxTokens = &v
	}
	if f.Preprocess {
		v := true
		ret.Preprocess = &v
	}
	if f.PreprocessTimeoutSeconds > 0 {
		v := f.PreprocessTimeoutSeconds
		ret.PreprocessTimeoutSec = &v
	}
	if text := strings.TrimSpace(f.AsyncNarratorPrompt); text != "" {
		ret.AsyncNarratorPrompt = text
	}
	if ret.Context == "" && ret.AgentID == "" && ret.Model == "" && ret.Effort == "" &&
		ret.Temperature == nil && ret.MaxTokens == nil && ret.Preprocess == nil &&
		ret.PreprocessTimeoutSec == nil && ret.AsyncNarratorPrompt == "" {
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
