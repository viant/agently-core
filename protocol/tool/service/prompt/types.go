package prompt

// ListInput is the request payload for prompt:list.
type ListInput struct{}

// ListItem is a single entry in a prompt:list response.
// Instruction content is intentionally omitted — descriptions are selection
// guidance only so that orchestrators can choose a profile without seeing its
// full instruction text.
type ListItem struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	AppliesTo   []string `json:"appliesTo,omitempty"`
	ToolBundles []string `json:"toolBundles,omitempty"`
	Template    string   `json:"template,omitempty"`
}

// ListOutput is the response payload for prompt:list.
type ListOutput struct {
	Profiles []ListItem `json:"profiles"`
}

// GetInput is the request payload for prompt:get.
type GetInput struct {
	ID              string `json:"id"`
	IncludeDocument *bool  `json:"includeDocument,omitempty"`
}

// GetOutput is the response payload for prompt:get.
// Messages are always returned regardless of includeDocument.
// includeDocument controls only whether messages are also injected into the
// current conversation via AddMessage().
type GetOutput struct {
	ID             string    `json:"id"`
	Name           string    `json:"name,omitempty"`
	Description    string    `json:"description,omitempty"`
	ToolBundles    []string  `json:"toolBundles,omitempty"`
	PreferredTools []string  `json:"preferredTools,omitempty"`
	Template       string    `json:"template,omitempty"`
	Resources      []string  `json:"resources,omitempty"`
	Messages       []Message `json:"messages,omitempty"`
	// Injected is true when includeDocument=true and messages were written into
	// the conversation.
	Injected bool `json:"injected,omitempty"`
}

// Message is a rendered role+content pair returned in GetOutput.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}
