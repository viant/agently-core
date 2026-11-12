package prompt

import (
	intent "github.com/viant/agently-core/protocol/intent"
	"github.com/viant/agently-core/protocol/primitive"
)

// ListInput is the request payload for prompt:list.
type ListInput struct{ primitive.CatalogQuery }

// ListItem is a single entry in a prompt:list response.
// Instruction content is intentionally omitted — descriptions are selection
// guidance only so that orchestrators can choose a profile without seeing its
// full instruction text.
type ListItem struct {
	URI              string                  `json:"uri,omitempty"`
	ID               string                  `json:"id"`
	Name             string                  `json:"name,omitempty"`
	Description      string                  `json:"description,omitempty"`
	AppliesTo        []string                `json:"appliesTo,omitempty"`
	EvidenceContract *EvidenceContract       `json:"evidenceContract,omitempty"`
	ToolBundles      []string                `json:"toolBundles,omitempty"`
	Template         string                  `json:"template,omitempty"`
	Templates        []string                `json:"templates,omitempty"`
	Knowledge        []intent.KnowledgeMatch `json:"knowledge,omitempty"`
}

// ListOutput is the response payload for prompt:list.
type ListOutput struct {
	Profiles   []ListItem `json:"profiles"`
	NextCursor string     `json:"nextCursor,omitempty"`
	Complete   bool       `json:"complete"`
	Total      int        `json:"total"`
}

// GetInput is the request payload for prompt:get.
type GetInput struct {
	ID              string `json:"id"`
	IncludeDocument *bool  `json:"includeDocument,omitempty" description:"Set true when following this playbook: inject its authored messages into the current conversation preserving system/user roles. False or omitted is inspection only."`
}

// GetOutput is the response payload for prompt:get.
// Inspection returns messages. Activation injects the authored messages and
// returns only their IDs so instruction text is not duplicated in tool output.
type GetOutput struct {
	MessageIDs       []string                `json:"messageIds,omitempty"`
	ID               string                  `json:"id"`
	Name             string                  `json:"name,omitempty"`
	Description      string                  `json:"description,omitempty"`
	EvidenceContract *EvidenceContract       `json:"evidenceContract,omitempty"`
	ToolBundles      []string                `json:"toolBundles,omitempty"`
	PreferredTools   []string                `json:"preferredTools,omitempty"`
	Template         string                  `json:"template,omitempty"`
	Templates        []string                `json:"templates,omitempty"`
	Resources        []string                `json:"resources,omitempty"`
	Knowledge        []intent.KnowledgeMatch `json:"knowledge,omitempty"`
	Messages         []Message               `json:"messages,omitempty"`
	// Injected is true when includeDocument=true and messages were written into
	// the conversation.
	Injected bool `json:"injected,omitempty"`
}

// Message is a rendered role+content pair returned in GetOutput.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type EvidenceContract struct {
	Required   []string `json:"required,omitempty"`
	Optional   []string `json:"optional,omitempty"`
	Forbidden  []string `json:"forbidden,omitempty"`
	Completion []string `json:"completion,omitempty"`
}
