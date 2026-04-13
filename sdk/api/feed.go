package api

// FeedSpec describes a tool feed loaded from workspace YAML.
type FeedSpec struct {
	ID         string                 `yaml:"id" json:"id"`
	Title      string                 `yaml:"title,omitempty" json:"title,omitempty"`
	Match      FeedMatch              `yaml:"match" json:"match"`
	Activation FeedActivation         `yaml:"activation,omitempty" json:"activation,omitempty"`
	DataSource map[string]interface{} `yaml:"dataSource,omitempty" json:"dataSource,omitempty"`
	UI         interface{}            `yaml:"ui,omitempty" json:"ui,omitempty"`
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
	FeedID    string `json:"feedId"`
	Title     string `json:"title"`
	ItemCount int    `json:"itemCount"`
	ToolName  string `json:"toolName,omitempty"`
}
