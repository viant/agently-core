package view

// Spec is a workspace-defined dynamic UI view descriptor.
// It does not duplicate the Forge Window payload; it points to an
// existing windowKey and adds model-facing metadata for routing.
type Spec struct {
	ID                 string       `json:"id" yaml:"id"`
	Title              string       `json:"title,omitempty" yaml:"title,omitempty"`
	Description        string       `json:"description,omitempty" yaml:"description,omitempty"`
	WindowKey          string       `json:"windowKey" yaml:"windowKey"`
	Presentation       string       `json:"presentation,omitempty" yaml:"presentation,omitempty"`
	Region             string       `json:"region,omitempty" yaml:"region,omitempty"`
	OpenMode           string       `json:"openMode,omitempty" yaml:"openMode,omitempty"`
	WorkspaceSharePct  int          `json:"workspaceSharePct,omitempty" yaml:"workspaceSharePct,omitempty"`
	WorkspaceMinHeight int          `json:"workspaceMinHeight,omitempty" yaml:"workspaceMinHeight,omitempty"`
	Parameters         []Parameter  `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Capabilities       Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`
}

type Parameter struct {
	Name     string `json:"name" yaml:"name"`
	Type     string `json:"type,omitempty" yaml:"type,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
	BindTo   string `json:"bindTo,omitempty" yaml:"bindTo,omitempty"`
}

type Capabilities struct {
	Show       bool `json:"show,omitempty" yaml:"show,omitempty"`
	Inspect    bool `json:"inspect,omitempty" yaml:"inspect,omitempty"`
	Datasource bool `json:"datasource,omitempty" yaml:"datasource,omitempty"`
}
