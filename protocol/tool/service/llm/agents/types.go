package agents

import "github.com/viant/agently-core/genai/llm"

// ListItem is a directory entry describing an agent option for selection.
type ListItem struct {
	ID               string                 `json:"id"`
	Name             string                 `json:"name,omitempty"`
	Description      string                 `json:"description,omitempty"`
	Summary          string                 `json:"summary,omitempty"`
	Internal         bool                   `json:"internal,omitempty"`
	Tags             []string               `json:"tags,omitempty"`
	Priority         int                    `json:"priority,omitempty"`
	Capabilities     map[string]interface{} `json:"capabilities,omitempty"`
	Source           string                 `json:"source,omitempty"` // internal | external
	Responsibilities []string               `json:"responsibilities,omitempty"`
	InScope          []string               `json:"inScope,omitempty"`
	OutOfScope       []string               `json:"outOfScope,omitempty"`
}

// ListOutput defines the response payload for agents:list.
type ListOutput struct {
	Items      []ListItem `json:"items"`
	ReuseNote  string     `json:"reuseNote,omitempty"`
	RunUsage   string     `json:"runUsage,omitempty"`
	NextAction string     `json:"nextAction,omitempty"`
}

// RunInput defines the request payload for agents:run.
// Note: Conversation/turn/user identifiers are derived from context; they are
// intentionally not part of the input contract.
type RunInput struct {
	AgentID   string                 `json:"agentId"`
	Objective string                 `json:"objective"`
	Context   map[string]interface{} `json:"context,omitempty"`
	Async     *bool                  `json:"async,omitempty" internal:"true"`
	// ConversationID optionally overrides the conversation identifier when
	// not already provided by context.
	ConversationID string `json:"conversationId,omitempty"`
	// Streaming is an optional hint. Runtime policy/capabilities decide final behavior.
	Streaming *bool `json:"streaming,omitempty"`
	// ModelPreferences optionally hints how to select a model for this
	// run when the agent supports model preferences. When omitted, the
	// agent's configured model selection is used.
	ModelPreferences *llm.ModelPreferences `json:"modelPreferences,omitempty"`
	// ReasoningEffort optionally overrides agent-level reasoning effort
	// (e.g., low|medium|high) for this run when supported by the backend.
	ReasoningEffort *string `json:"reasoningEffort,omitempty"`
}

// RunOutput defines the response payload for agents:run.
// Depending on routing (internal vs external), different handles will be set.
type RunOutput struct {
	Answer          string   `json:"answer"`
	Status          string   `json:"status,omitempty"`
	Error           string   `json:"error,omitempty"`
	ConversationID  string   `json:"conversationId,omitempty"`
	MessageID       string   `json:"messageId,omitempty"`
	TaskID          string   `json:"taskId,omitempty"`
	ContextID       string   `json:"contextId,omitempty"`
	StreamSupported bool     `json:"streamSupported,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

// StatusInput queries the status of one child conversation or all children for
// a parent conversation / turn pair.
type StatusInput struct {
	ConversationID       string `json:"conversationId,omitempty"`
	ParentConversationID string `json:"parentConversationId,omitempty"`
	ParentTurnID         string `json:"parentTurnId,omitempty"`
}

type StatusItem struct {
	ConversationID        string `json:"conversationId,omitempty"`
	ParentConversationID  string `json:"parentConversationId,omitempty"`
	ParentTurnID          string `json:"parentTurnId,omitempty"`
	AgentID               string `json:"agentId,omitempty"`
	Status                string `json:"status,omitempty"`
	CreatedAt             string `json:"createdAt,omitempty"`
	UpdatedAt             string `json:"updatedAt,omitempty"`
	LastAssistantPreamble string `json:"lastAssistantPreamble,omitempty"`
	LastAssistantResponse string `json:"lastAssistantResponse,omitempty"`
	HasFinalResponse      bool   `json:"hasFinalResponse,omitempty"`
	LastMessageAt         string `json:"lastMessageAt,omitempty"`
}

type StatusOutput struct {
	ConversationID string       `json:"conversationId,omitempty"`
	Status         string       `json:"status,omitempty"`
	Items          []StatusItem `json:"items,omitempty"`
}

type CancelInput struct {
	ConversationID string `json:"conversationId"`
}

type CancelOutput struct {
	Status string `json:"status,omitempty"`
}

// MeOutput provides minimal execution context details.
type MeOutput struct {
	ConversationID string `json:"conversationId,omitempty"`
	AgentName      string `json:"agentName,omitempty"`
	Model          string `json:"model,omitempty"`
}
