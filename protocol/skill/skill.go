package skill

import "strings"

type Frontmatter struct {
	Name                     string            `yaml:"name"`
	Description              string            `yaml:"description"`
	License                  string            `yaml:"license,omitempty"`
	Metadata                 map[string]string `yaml:"metadata,omitempty"`
	Context                  string            `yaml:"context,omitempty"`
	AllowedTools             string            `yaml:"allowed-tools,omitempty"`
	Model                    string            `yaml:"model,omitempty"`
	Effort                   string            `yaml:"effort,omitempty"`
	Temperature              *float64          `yaml:"temperature,omitempty"`
	MaxTokens                int               `yaml:"max-tokens,omitempty"`
	Preprocess               bool              `yaml:"preprocess,omitempty"`
	PreprocessTimeoutSeconds int               `yaml:"preprocess-timeout,omitempty"`
	// AsyncNarratorPrompt overrides the agent-level and workspace-level
	// async narrator system prompt when this skill is the active skill
	// for a turn. Resolution order (highest precedence first):
	// active-skill → agent → workspace default. Empty → fall through
	// to the next level.
	AsyncNarratorPrompt string         `yaml:"async-narrator-prompt,omitempty"`
	Raw                 map[string]any `yaml:"-"`
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
	case "", "inline":
		return "inline"
	case "fork", "detach":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "inline"
	}
}

func (f Frontmatter) ContextMode() string {
	return NormalizeContextMode(f.Context)
}
