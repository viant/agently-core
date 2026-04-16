package prompt

// Profile is a scenario configuration unit pairing instruction messages,
// tool bundles, and an output template.
type Profile struct {
	ID           string     `yaml:"id"                       json:"id"`
	Name         string     `yaml:"name,omitempty"           json:"name,omitempty"`
	Description  string     `yaml:"description,omitempty"    json:"description,omitempty"`
	AppliesTo    []string   `yaml:"appliesTo,omitempty"      json:"appliesTo,omitempty"`
	Messages     []Message  `yaml:"messages,omitempty"       json:"messages,omitempty"`
	Instructions string     `yaml:"instructions,omitempty"   json:"instructions,omitempty"`
	MCP          *MCPSource `yaml:"mcp,omitempty"            json:"mcp,omitempty"`
	// ToolBundles lists the tool-bundle ids that are activated for the worker
	// when this profile is applied.  Runtime enforcement, not access control.
	ToolBundles    []string   `yaml:"toolBundles,omitempty"    json:"toolBundles,omitempty"`
	PreferredTools []string   `yaml:"preferredTools,omitempty" json:"preferredTools,omitempty"`
	Template       string     `yaml:"template,omitempty"       json:"template,omitempty"`
	Resources      []string   `yaml:"resources,omitempty"      json:"resources,omitempty"`
	Expansion      *Expansion `yaml:"expansion,omitempty"      json:"expansion,omitempty"`
}

// Message is a single role+content instruction, aligned with MCP PromptMessage.
type Message struct {
	Role string `yaml:"role"           json:"role"`
	Text string `yaml:"text,omitempty" json:"text,omitempty"`
	URI  string `yaml:"uri,omitempty"  json:"uri,omitempty"`
}

// MCPSource fetches instructions from an MCP server prompt.
type MCPSource struct {
	Server string            `yaml:"server"         json:"server"`
	Prompt string            `yaml:"prompt"         json:"prompt"`
	Args   map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

// Expansion configures optional sidecar LLM synthesis at delegation time.
type Expansion struct {
	Mode      string `yaml:"mode"                json:"mode"`
	Model     string `yaml:"model,omitempty"     json:"model,omitempty"`
	MaxTokens int    `yaml:"maxTokens,omitempty" json:"maxTokens,omitempty"`
}

// EffectiveMessages returns the messages to render.
// Messages takes priority over Instructions. Returns nil for MCP-only profiles.
func (p *Profile) EffectiveMessages() []Message {
	if len(p.Messages) > 0 {
		return p.Messages
	}
	if p.Instructions != "" {
		return []Message{{Role: "system", Text: p.Instructions}}
	}
	return nil
}
