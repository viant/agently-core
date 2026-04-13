package api

import (
	"encoding/json"
	"time"
)

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

type TurnStatus string

const (
	TurnStatusQueued         TurnStatus = "queued"
	TurnStatusRunning        TurnStatus = "running"
	TurnStatusWaitingForUser TurnStatus = "waiting_for_user"
	TurnStatusCompleted      TurnStatus = "completed"
	TurnStatusFailed         TurnStatus = "failed"
	TurnStatusCanceled       TurnStatus = "canceled"
)

type UserMessageState struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content,omitempty"`
}

type AssistantState struct {
	Preamble *AssistantMessageState `json:"preamble,omitempty"`
	Final    *AssistantMessageState `json:"final,omitempty"`
}

type AssistantMessageState struct {
	MessageID string `json:"messageId"`
	Content   string `json:"content,omitempty"`
}

type ExecutionState struct {
	Pages          []*ExecutionPageState `json:"pages"`
	ActivePageIdx  int                   `json:"activePageIndex"`
	TotalElapsedMs int64                 `json:"totalElapsedMs"`
}

type ExecutionPageState struct {
	PageID                  string            `json:"pageId"`
	AssistantMessageID      string            `json:"assistantMessageId"`
	ParentMessageID         string            `json:"parentMessageId"`
	TurnID                  string            `json:"turnId"`
	Iteration               int               `json:"iteration"`
	Mode                    string            `json:"mode,omitempty"`
	Status                  string            `json:"status,omitempty"`
	ModelSteps              []*ModelStepState `json:"modelSteps,omitempty"`
	ToolSteps               []*ToolStepState  `json:"toolSteps,omitempty"`
	PreambleMessageID       string            `json:"preambleMessageId,omitempty"`
	FinalAssistantMessageID string            `json:"finalAssistantMessageId,omitempty"`
	Preamble                string            `json:"preamble,omitempty"`
	Content                 string            `json:"content,omitempty"`
	FinalResponse           bool              `json:"finalResponse"`
}

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

type ToolStepState struct {
	ToolCallID                string               `json:"toolCallId"`
	ToolMessageID             string               `json:"toolMessageId"`
	ToolName                  string               `json:"toolName"`
	OperationID               string               `json:"operationId,omitempty"`
	Status                    string               `json:"status,omitempty"`
	RequestPayloadID          string               `json:"requestPayloadId,omitempty"`
	ResponsePayloadID         string               `json:"responsePayloadId,omitempty"`
	RequestPayload            json.RawMessage      `json:"requestPayload,omitempty"`
	ResponsePayload           json.RawMessage      `json:"responsePayload,omitempty"`
	LinkedConversationID      string               `json:"linkedConversationId,omitempty"`
	LinkedConversationAgentID string               `json:"linkedConversationAgentId,omitempty"`
	LinkedConversationTitle   string               `json:"linkedConversationTitle,omitempty"`
	StartedAt                 *time.Time           `json:"startedAt,omitempty"`
	CompletedAt               *time.Time           `json:"completedAt,omitempty"`
	AsyncOperation            *AsyncOperationState `json:"asyncOperation,omitempty"`
}

type AsyncOperationState struct {
	OperationID string          `json:"operationId"`
	Status      string          `json:"status,omitempty"`
	Message     string          `json:"message,omitempty"`
	Error       string          `json:"error,omitempty"`
	Response    json.RawMessage `json:"response,omitempty"`
}

type ElicitationState struct {
	ElicitationID   string            `json:"elicitationId"`
	Status          ElicitationStatus `json:"status"`
	Message         string            `json:"message,omitempty"`
	RequestedSchema json.RawMessage   `json:"requestedSchema,omitempty"`
	CallbackURL     string            `json:"callbackUrl,omitempty"`
	ResponsePayload json.RawMessage   `json:"responsePayload,omitempty"`
}

type ElicitationStatus string

const (
	ElicitationStatusPending  ElicitationStatus = "pending"
	ElicitationStatusAccepted ElicitationStatus = "accepted"
	ElicitationStatusDeclined ElicitationStatus = "declined"
	ElicitationStatusCanceled ElicitationStatus = "canceled"
)

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

type PlanFeedPayload struct {
	Explanation string      `json:"explanation,omitempty"`
	Steps       []*PlanStep `json:"steps,omitempty"`
}

type PlanStep struct {
	ID      string `json:"id,omitempty"`
	Step    string `json:"step"`
	Status  string `json:"status,omitempty"`
	Details string `json:"details,omitempty"`
}

type UsageSummary struct {
	TotalInputTokens  int `json:"totalInputTokens,omitempty"`
	TotalOutputTokens int `json:"totalOutputTokens,omitempty"`
}

type ConversationStateResponse struct {
	SchemaVersion string             `json:"schemaVersion"`
	Conversation  *ConversationState `json:"conversation"`
	Feeds         []*ActiveFeedState `json:"feeds,omitempty"`
	Usage         *UsageSummary      `json:"usage,omitempty"`
	EventCursor   string             `json:"eventCursor,omitempty"`
}
