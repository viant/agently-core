// Package agui implements the supported AG-UI 1.0 chat boundary.
// Schema pin: ag-ui-protocol/ag-ui@e60019d258cf43ecc5ad19e8d374f1c31cbf2b94,
// spec/1.0/schema.json; matching @ag-ui/core version 1.0.1.
package agui

import "encoding/json"

const ProtocolVersion = "1.0"
const ExtensionVersion = "1"

type RunAgentInput struct {
	ThreadID        string          `json:"threadId"`
	RunID           string          `json:"runId"`
	ProtocolVersion string          `json:"protocolVersion,omitempty"`
	ParentRunID     string          `json:"parentRunId,omitempty"`
	State           json.RawMessage `json:"state,omitempty"`
	Messages        []Message       `json:"messages"`
	Tools           []Tool          `json:"tools,omitempty"`
	Context         []Context       `json:"context,omitempty"`
	ForwardedProps  json.RawMessage `json:"forwardedProps,omitempty"`
	Resume          json.RawMessage `json:"resume,omitempty"`
}

// Content remains raw to preserve and reject unsupported multimodal inputs at
// the HTTP boundary without silently coercing them into chat text.
type Message struct {
	rawInput       json.RawMessage
	ActivityType   string          `json:"activityType,omitempty"`
	EncryptedValue *string         `json:"encryptedValue,omitempty"`
	SubagentRunID  *string         `json:"subagentRunId,omitempty"`
	ID             string          `json:"id"`
	Role           string          `json:"role"`
	Content        json.RawMessage `json:"content,omitempty"`
	Name           string          `json:"name,omitempty"`
	ToolCallID     string          `json:"toolCallId,omitempty"`
	ToolCalls      json.RawMessage `json:"toolCalls,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Error          *string         `json:"error,omitempty"`
}
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}
type Context struct {
	Description string `json:"description"`
	Value       string `json:"value"`
}
type ForwardedProps struct {
	Agently *Extension `json:"agently,omitempty"`
}
type Extension struct {
	Version   string            `json:"version"`
	Operation string            `json:"operation"`
	RequestID string            `json:"requestId,omitempty"`
	Payload   *ExecutionPayload `json:"payload,omitempty"`
}
type ExecutionPayload struct {
	AgentID          string          `json:"agentId,omitempty"`
	Model            string          `json:"model,omitempty"`
	BackendTools     []string        `json:"backendTools,omitempty"`
	ToolBundles      []string        `json:"toolBundles,omitempty"`
	AutoSelectTools  *bool           `json:"autoSelectTools,omitempty"`
	AutoSummarize    *bool           `json:"autoSummarize,omitempty"`
	DisableChains    *bool           `json:"disableChains,omitempty"`
	AllowedChains    []string        `json:"allowedChains,omitempty"`
	ResourceURIs     []string        `json:"resourceURIs,omitempty"`
	ToolCallExposure *string         `json:"toolCallExposure,omitempty"`
	UseServerState   *bool           `json:"useServerState,omitempty"`
	ReasoningEffort  *string         `json:"reasoningEffort,omitempty"`
	DisplayQuery     *string         `json:"displayQuery,omitempty"`
	Context          json.RawMessage `json:"context,omitempty"`
	Attachments      []AttachmentRef `json:"attachments,omitempty"`
}

// AttachmentRef references files admitted by the existing authenticated file
// service. Binary contents use the standard content model or file transfer API.
type AttachmentRef struct {
	Name          string `json:"name,omitempty"`
	URI           string `json:"uri"`
	Mime          string `json:"mime,omitempty"`
	StagingFolder string `json:"stagingFolder,omitempty"`
}
type AgentCapabilities map[string]any
type CapabilitiesValue struct {
	Version      string            `json:"version"`
	Capabilities AgentCapabilities `json:"capabilities"`
}

// Event is the producer's supported event union. Only the fields belonging to
// the event discriminator are populated; schema-1.0.json is the wire authority.
type Event struct {
	applied bool
	// Standard carries a validated full event variant while retaining the legacy producer API.
	Standard        json.RawMessage `json:"-"`
	Type            string          `json:"type"`
	Timestamp       int64           `json:"timestamp,omitempty"`
	ThreadID        string          `json:"threadId,omitempty"`
	RunID           string          `json:"runId,omitempty"`
	ProtocolVersion string          `json:"protocolVersion,omitempty"`
	MessageID       string          `json:"messageId,omitempty"`
	Role            string          `json:"role,omitempty"`
	Delta           string          `json:"delta,omitempty"`
	ToolCallID      string          `json:"toolCallId,omitempty"`
	ToolCallName    string          `json:"toolCallName,omitempty"`
	ParentMessageID string          `json:"parentMessageId,omitempty"`
	Content         *string         `json:"content,omitempty"`
	Message         string          `json:"message,omitempty"`
	Code            string          `json:"code,omitempty"`
	Outcome         *Outcome        `json:"outcome,omitempty"`
	Name            string          `json:"name,omitempty"`
	Value           any             `json:"value,omitempty"`
}
type Outcome struct {
	Type string `json:"type"`
}
type QueueValue struct {
	Version            string `json:"version"`
	TurnID             string `json:"turnId,omitempty"`
	Sequence           string `json:"sequence,omitempty"`
	Origin             string `json:"origin,omitempty"`
	StartedByMessageID string `json:"startedByMessageId,omitempty"`
}
type ProgressValue struct {
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	MessageID  string `json:"messageId,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Status     string `json:"status,omitempty"`
	Content    string `json:"content,omitempty"`
	Source     string `json:"source,omitempty"`
}
