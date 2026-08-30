package api

// FeedSpec describes a tool feed loaded from workspace YAML.
type FeedSpec struct {
	ID            string                 `yaml:"id" json:"id"`
	Title         string                 `yaml:"title,omitempty" json:"title,omitempty"`
	DeveloperOnly bool                   `yaml:"developerOnly,omitempty" json:"developerOnly,omitempty"`
	Presentation  *FeedPresentation      `yaml:"presentation,omitempty" json:"presentation,omitempty"`
	Match         FeedMatch              `yaml:"match" json:"match"`
	Activation    FeedActivation         `yaml:"activation,omitempty" json:"activation,omitempty"`
	DataSource    map[string]interface{} `yaml:"dataSource,omitempty" json:"dataSource,omitempty"`
	UI            interface{}            `yaml:"ui,omitempty" json:"ui,omitempty"`
}

// FeedPresentation contains optional, workspace-owned visual hints. Clients
// must use neutral defaults when it is absent or contains an unknown token.
type FeedPresentation struct {
	Icon              string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Accent            string   `yaml:"accent,omitempty" json:"accent,omitempty"`
	SuppressReportIDs []string `yaml:"suppressReportIds,omitempty" json:"suppressReportIds,omitempty"`
	// Target selects where this specific feed is rendered. Empty/auto keeps
	// the legacy client-selected placement. Supported explicit targets are
	// inline, workspace, and detached.
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
}

// FeedMatch defines which tool calls trigger this feed.
type FeedMatch struct {
	Service string `yaml:"service" json:"service"`
	Method  string `yaml:"method" json:"method"`
}

// FeedActivation controls how feed data is gathered.
type FeedActivation struct {
	Kind    string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Scope   string `yaml:"scope,omitempty" json:"scope,omitempty"`
	Service string `yaml:"service,omitempty" json:"service,omitempty"`
	Method  string `yaml:"method,omitempty" json:"method,omitempty"`
}

// FeedState tracks active feeds for a conversation.
type FeedState struct {
	FeedID        string            `json:"feedId"`
	TurnID        string            `json:"turnId,omitempty"`
	Title         string            `json:"title"`
	DeveloperOnly bool              `json:"developerOnly,omitempty"`
	Presentation  *FeedPresentation `json:"presentation,omitempty"`
	ItemCount     int               `json:"itemCount"`
	ToolName      string            `json:"toolName,omitempty"`
}
