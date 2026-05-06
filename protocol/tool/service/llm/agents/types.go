package agents

import (
	"github.com/viant/agently-core/genai/llm"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	agruntime "github.com/viant/agently-core/runtime"
	intakesvc "github.com/viant/agently-core/service/intake"
)

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

type TopologyInput struct {
	AgentIDs []string `json:"agentIds,omitempty"`
}

type DelegationInfo struct {
	Enabled           bool `json:"enabled,omitempty"`
	MaxSameAgentDepth int  `json:"maxSameAgentDepth,omitempty"`
}

type TopologyItem struct {
	ID               string         `json:"id"`
	Name             string         `json:"name,omitempty"`
	Description      string         `json:"description,omitempty"`
	Internal         bool           `json:"internal,omitempty"`
	Skills           []string       `json:"skills,omitempty"`
	ToolBundles      []string       `json:"toolBundles,omitempty"`
	ToolNames        []string       `json:"toolNames,omitempty"`
	TemplateBundles  []string       `json:"templateBundles,omitempty"`
	PromptProfiles   []string       `json:"promptProfiles,omitempty"`
	PlannerEnabled   bool           `json:"plannerEnabled,omitempty"`
	PlannerAgentID   string         `json:"plannerAgentId,omitempty"`
	Responsibilities []string       `json:"responsibilities,omitempty"`
	InScope          []string       `json:"inScope,omitempty"`
	OutOfScope       []string       `json:"outOfScope,omitempty"`
	Delegation       DelegationInfo `json:"delegation,omitempty"`
}

type TopologyOutput struct {
	Items []TopologyItem `json:"items,omitempty"`
}

type ToolDetailsInput struct {
	Names []string `json:"names,omitempty"`
}

type ToolDetailsItem struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
	Required     []string               `json:"required,omitempty"`
	OutputSchema map[string]interface{} `json:"outputSchema,omitempty"`
	Cacheable    bool                   `json:"cacheable,omitempty"`
}

type ToolDetailsOutput struct {
	Items []ToolDetailsItem `json:"items,omitempty"`
}

// RunInput defines the request payload for agents:run.
// Note: Conversation/turn/user identifiers are derived from context; they are
// intentionally not part of the input contract.
type RunInput struct {
	AgentID       string                 `json:"agentId"`
	Agent         *agentmdl.Agent        `json:"agent,omitempty" internal:"true"`
	Objective     string                 `json:"objective"`
	Context       map[string]interface{} `json:"context,omitempty"`
	Runtime       *agruntime.Context     `json:"runtime,omitempty"`
	ExecutionMode string                 `json:"executionMode,omitempty"`
	Async         *bool                  `json:"async,omitempty" internal:"true"`
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
	// PromptProfileId optionally selects a scenario profile whose instructions,
	// tool bundles, and output template are applied to the child conversation.
	// When absent, behaviour is identical to today.
	PromptProfileId string `json:"promptProfileId,omitempty"`
	// ToolBundles optionally appends tool bundle ids on top of whatever the
	// profile floor already provides.  Safe to leave empty.
	ToolBundles []string `json:"toolBundles,omitempty"`
	// TemplateId optionally overrides the output template selected by the
	// profile (highest-priority tier in the three-tier resolution chain).
	TemplateId string `json:"templateId,omitempty"`

	// WorkspaceIntake optionally pre-provides the workspace-intake result for
	// this run. When present and validated, the runtime SKIPS the workspace-
	// intake LLM call entirely and uses this value as the turn's intake Context
	// (annotated as Source="caller-provided"). Validation rules are identical
	// to workspace intake's own output — SelectedAgentID must be in the
	// authorized agent set, and AppendToolBundles must be on the workspace
	// allowlist. When any
	// validation fails, the override is dropped (with a diagnostic) and
	// workspace intake runs normally.
	//
	// Use cases: programmatic clients with their own classifier, UI that
	// pre-populates routing fields, cached prior turns, or cross-conversation
	// seeds. See intake-impt.md §9 skip-rule (c).
	WorkspaceIntake *intakesvc.Context `json:"workspaceIntake,omitempty"`
}

// StartInput launches an agent asynchronously and returns a conversation handle.
// It shares the same public fields as RunInput, but the service forces async=true.
type StartInput = RunInput

type StartOutput struct {
	ConversationID    string   `json:"conversationId,omitempty"`
	Status            string   `json:"status,omitempty"`
	ResultMode        string   `json:"resultMode,omitempty"`
	Message           string   `json:"message,omitempty"`
	AssistantResponse string   `json:"assistantResponse,omitempty"`
	TaskID            string   `json:"taskId,omitempty"`
	ContextID         string   `json:"contextId,omitempty"`
	StreamSupported   bool     `json:"streamSupported,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

// RunOutput defines the response payload for agents:run.
// Depending on routing (internal vs external), different handles will be set.
type RunOutput struct {
	Answer          string   `json:"answer"`
	Status          string   `json:"status,omitempty"`
	ResultMode      string   `json:"resultMode,omitempty"`
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
	ConversationID         string `json:"conversationId,omitempty"`
	ParentConversationID   string `json:"parentConversationId,omitempty"`
	ParentTurnID           string `json:"parentTurnId,omitempty"`
	AgentID                string `json:"agentId,omitempty"`
	Status                 string `json:"status,omitempty"`
	RawStatus              string `json:"rawStatus,omitempty"`
	Terminal               bool   `json:"terminal,omitempty"`
	Error                  string `json:"error,omitempty"`
	CreatedAt              string `json:"createdAt,omitempty"`
	UpdatedAt              string `json:"updatedAt,omitempty"`
	LastAssistantNarration string `json:"lastAssistantNarration,omitempty"`
	LastAssistantResponse  string `json:"lastAssistantResponse,omitempty"`
	HasFinalResponse       bool   `json:"hasFinalResponse,omitempty"`
	LastMessageAt          string `json:"lastMessageAt,omitempty"`
}

type StatusOutput struct {
	ConversationID string `json:"conversationId,omitempty"`
	Status         string `json:"status,omitempty"`
	RawStatus      string `json:"rawStatus,omitempty"`
	Terminal       bool   `json:"terminal,omitempty"`
	Error          string `json:"error,omitempty"`
	Message        string `json:"message,omitempty"`
	MessageKind    string `json:"messageKind,omitempty"`
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
