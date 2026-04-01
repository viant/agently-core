package sdk

import (
	"encoding/json"
	"time"
)

// Canonical state types for the render pipeline.
//
// These types define the single source of truth for conversation rendering.
// SDK reducers produce ConversationState from stream events and transcript
// snapshots; UI renderers consume it via selectors. No component should
// synthesize execution structure outside these types.

// ConversationState is the top-level canonical state for a conversation.
// ActiveFeedState describes a tool feed matched from the transcript.
type ActiveFeedState struct {
	FeedID    string          `json:"feedId"`
	Title     string          `json:"title"`
	ItemCount int             `json:"itemCount"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type ConversationState struct {
	ConversationID string             `json:"conversationId"`
	Turns          []*TurnState       `json:"turns"`
	Feeds          []*ActiveFeedState `json:"feeds,omitempty"`
}

// TurnState is the canonical representation of a single conversation turn.
type TurnState struct {
	TurnID              string                     `json:"turnId"`
	Status              TurnStatus                 `json:"status"`
	User                *UserMessageState          `json:"user,omitempty"`
	Execution           *ExecutionState            `json:"execution,omitempty"`
	Assistant           *AssistantState            `json:"assistant,omitempty"`
	Elicitation         *ElicitationState          `json:"elicitation,omitempty"`
	LinkedConversations []*LinkedConversationState `json:"linkedConversations,omitempty"`
	CreatedAt           time.Time                  `json:"createdAt,omitempty"`
	QueueSeq            int                        `json:"queueSeq,omitempty"`
	StartedByMessageID  string                     `json:"startedByMessageId,omitempty"`
}

// TurnStatus enumerates canonical turn lifecycle states.
type TurnStatus string

const (
	TurnStatusQueued         TurnStatus = "queued"
	TurnStatusRunning        TurnStatus = "running"
	TurnStatusWaitingForUser TurnStatus = "waiting_for_user"
	TurnStatusCompleted      TurnStatus = "completed"
	TurnStatusFailed         TurnStatus = "failed"
	TurnStatusCanceled       TurnStatus = "canceled"
)

// UserMessageState represents the user message that initiated a turn.
type UserMessageState struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content,omitempty"`
}

// AssistantState holds preamble and final assistant content for a turn.
type AssistantState struct {
	Preamble *AssistantMessageState `json:"preamble,omitempty"`
	Final    *AssistantMessageState `json:"final,omitempty"`
}

// AssistantMessageState represents a single assistant message fragment.
type AssistantMessageState struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content,omitempty"`
}

// ExecutionState aggregates all execution pages (iterations) within a turn.
type ExecutionState struct {
	Pages          []*ExecutionPageState `json:"pages"`
	ActivePageIdx  int                   `json:"activePageIndex"`
	TotalElapsedMs int64                 `json:"totalElapsedMs"`
}

// ExecutionPageState is one iteration of the ReAct loop (model call + tool calls).
type ExecutionPageState struct {
	PageID                  string            `json:"pageId"`
	AssistantMessageID      string            `json:"assistantMessageId"`
	ParentMessageID         string            `json:"parentMessageId"`
	TurnID                  string            `json:"turnId"`
	Iteration               int               `json:"iteration"`
	Mode                    string            `json:"mode,omitempty"` // "" = normal, "summary" = summary-pass
	Status                  string            `json:"status,omitempty"`
	ModelSteps              []*ModelStepState `json:"modelSteps,omitempty"`
	ToolSteps               []*ToolStepState  `json:"toolSteps,omitempty"`
	PreambleMessageID       string            `json:"preambleMessageId,omitempty"`
	FinalAssistantMessageID string            `json:"finalAssistantMessageId,omitempty"`
	Preamble                string            `json:"preamble,omitempty"`
	Content                 string            `json:"content,omitempty"`
	FinalResponse           bool              `json:"finalResponse"`
}

// ModelStepState represents a single LLM call within an execution page.
type ModelStepState struct {
	ModelCallID               string          `json:"modelCallId"`
	AssistantMessageID        string          `json:"assistantMessageId"`
	Provider                  string          `json:"provider,omitempty"`
	Model                     string          `json:"model,omitempty"`
	Status                    string          `json:"status,omitempty"`
	RequestPayloadID          string          `json:"requestPayloadId,omitempty"`
	ResponsePayloadID         string          `json:"responsePayloadId,omitempty"`
	ProviderRequestPayloadID  string          `json:"providerRequestPayloadId,omitempty"`
	ProviderResponsePayloadID string          `json:"providerResponsePayloadId,omitempty"`
	StreamPayloadID           string          `json:"streamPayloadId,omitempty"`
	RequestPayload            json.RawMessage `json:"requestPayload,omitempty"`
	ResponsePayload           json.RawMessage `json:"responsePayload,omitempty"`
	ProviderRequestPayload    json.RawMessage `json:"providerRequestPayload,omitempty"`
	ProviderResponsePayload   json.RawMessage `json:"providerResponsePayload,omitempty"`
	StreamPayload             json.RawMessage `json:"streamPayload,omitempty"`
	StartedAt                 *time.Time      `json:"startedAt,omitempty"`
	CompletedAt               *time.Time      `json:"completedAt,omitempty"`
}

// ToolStepState represents a single tool invocation within an execution page.
type ToolStepState struct {
	ToolCallID                string          `json:"toolCallId"`
	ToolMessageID             string          `json:"toolMessageId"`
	ToolName                  string          `json:"toolName"`
	Status                    string          `json:"status,omitempty"`
	RequestPayloadID          string          `json:"requestPayloadId,omitempty"`
	ResponsePayloadID         string          `json:"responsePayloadId,omitempty"`
	RequestPayload            json.RawMessage `json:"requestPayload,omitempty"`
	ResponsePayload           json.RawMessage `json:"responsePayload,omitempty"`
	LinkedConversationID      string          `json:"linkedConversationId,omitempty"`
	LinkedConversationAgentID string          `json:"linkedConversationAgentId,omitempty"`
	LinkedConversationTitle   string          `json:"linkedConversationTitle,omitempty"`
	StartedAt                 *time.Time      `json:"startedAt,omitempty"`
	CompletedAt               *time.Time      `json:"completedAt,omitempty"`
}

// ElicitationState represents a pending or resolved elicitation within a turn.
type ElicitationState struct {
	ElicitationID   string            `json:"elicitationId"`
	Status          ElicitationStatus `json:"status"`
	Message         string            `json:"message,omitempty"`
	RequestedSchema json.RawMessage   `json:"requestedSchema,omitempty"`
	CallbackURL     string            `json:"callbackUrl,omitempty"`
	ResponsePayload json.RawMessage   `json:"responsePayload,omitempty"`
}

// ElicitationStatus enumerates elicitation lifecycle states.
type ElicitationStatus string

const (
	ElicitationStatusPending  ElicitationStatus = "pending"
	ElicitationStatusAccepted ElicitationStatus = "accepted"
	ElicitationStatusDeclined ElicitationStatus = "declined"
	ElicitationStatusCanceled ElicitationStatus = "canceled"
)

// LinkedConversationState represents a child conversation linked to a parent turn.
type LinkedConversationState struct {
	ConversationID       string     `json:"conversationId"`
	ParentConversationID string     `json:"parentConversationId,omitempty"`
	ParentTurnID         string     `json:"parentTurnId,omitempty"`
	ToolCallID           string     `json:"toolCallId,omitempty"`
	AgentID              string     `json:"agentId,omitempty"`
	Title                string     `json:"title,omitempty"`
	Status               string     `json:"status,omitempty"`
	Response             string     `json:"response,omitempty"`
	CreatedAt            time.Time  `json:"createdAt,omitempty"`
	UpdatedAt            *time.Time `json:"updatedAt,omitempty"`
}

// PlanFeedPayload is the typed payload for plan tool feeds.
// It replaces untyped map probing for explanation/steps in feed rendering.
type PlanFeedPayload struct {
	Explanation string      `json:"explanation,omitempty"`
	Steps       []*PlanStep `json:"steps,omitempty"`
}

// PlanStep is one step within a PlanFeedPayload.
type PlanStep struct {
	ID      string `json:"id,omitempty"`
	Step    string `json:"step"`
	Status  string `json:"status,omitempty"`
	Details string `json:"details,omitempty"`
}

// UsageSummary aggregates token usage for a conversation transcript.
type UsageSummary struct {
	TotalInputTokens  int `json:"totalInputTokens,omitempty"`
	TotalOutputTokens int `json:"totalOutputTokens,omitempty"`
}

// ConversationStateResponse is the formal wire contract returned by GetTranscript
// and GetLiveState. It wraps ConversationState with a schema version, resolved
// feeds, optional usage, and an event cursor for SSE reconnection.
type ConversationStateResponse struct {
	SchemaVersion string             `json:"schemaVersion"`
	Conversation  *ConversationState `json:"conversation"`
	Feeds         []*ActiveFeedState `json:"feeds,omitempty"`
	Usage         *UsageSummary      `json:"usage,omitempty"`
	// EventCursor is an opaque position token in the event stream.
	// On SSE reconnect the client passes this to resume streaming from
	// exactly this point without replaying prior events.
	EventCursor string `json:"eventCursor,omitempty"`
}
