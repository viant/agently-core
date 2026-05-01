package streaming

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
)

type EventType string

const (
	// Stream delta types — fine-grained provider output.
	EventTypeTextDelta      EventType = "text_delta"
	EventTypeReasoningDelta EventType = "reasoning_delta"
	EventTypeToolCallDelta  EventType = "tool_call_delta"
	EventTypeError          EventType = "error"

	// Control events (patch-based updates).
	EventTypeControl EventType = "control"

	// Turn lifecycle.
	EventTypeTurnStarted   EventType = "turn_started"
	EventTypeTurnCompleted EventType = "turn_completed"
	EventTypeTurnFailed    EventType = "turn_failed"
	EventTypeTurnCanceled  EventType = "turn_canceled"
	// EventTypeTurnQueued is emitted when a new turn is enqueued behind an active turn.
	EventTypeTurnQueued EventType = "turn_queued"

	// Model lifecycle.
	EventTypeModelStarted   EventType = "model_started"
	EventTypeModelCompleted EventType = "model_completed"

	// Assistant content (aggregated).
	//
	// EventTypeNarration carries ephemeral assistant-side text — formerly
	// split conceptually between "preamble" (model-emitted, pre-tool-call)
	// and "narration" (runtime-emitted, during-tool-call). These are one
	// concept: running commentary. Wire payload fields: messageId,
	// content, and optionally toolCallId when emitted by the async
	// narrator pipeline.
	EventTypeNarration EventType = "narration"

	// EventTypeAssistant fires for a real turn message row (user or
	// assistant, NOT interim narration). One event type covers all
	// message appends — the `patch.role` field distinguishes user vs.
	// assistant. Replaces legacy final/control message events with a
	// single semantic surface.
	//
	// Contract:
	//   - Idempotent by messageId. First emission creates the client
	//     bubble; subsequent emissions update its fields.
	//   - Carries semantic fields: role (in patch), content, mode,
	//     status (in patch), sequence (in patch), createdAt. Never
	//     raw DB column diffs.
	//   - Fires only for real messages (interim=0). Interim
	//     commentary flows through `narration`.
	//   - A turn may carry any number of these; NONE is "the final".
	//     End-of-turn is signaled ONLY by EventTypeTurnCompleted /
	//     EventTypeTurnFailed / EventTypeTurnCanceled.
	//
	// The wire value is `"assistant"` for historical / path clarity
	// on the main case (assistant message append); user messages
	// carry `patch.role = "user"` on the same event type.
	EventTypeAssistant EventType = "assistant"

	// EventTypeMessageAppended is an alias of EventTypeAssistant kept
	// for in-flight session code that typed the name before the rename
	// settled. Deprecated: use EventTypeAssistant.
	EventTypeMessageAppended = EventTypeAssistant

	// Tool call lifecycle.
	EventTypeToolCallsPlanned  EventType = "tool_calls_planned"
	EventTypeToolCallStarted   EventType = "tool_call_started"
	EventTypeToolCallWaiting   EventType = "tool_call_waiting"
	EventTypeToolCallCompleted EventType = "tool_call_completed"
	EventTypeToolCallFailed    EventType = "tool_call_failed"
	EventTypeToolCallCanceled  EventType = "tool_call_canceled"

	// Stream metadata / completion.
	EventTypeItemCompleted EventType = "item_completed"
	EventTypeUsage         EventType = "usage"

	// Elicitation lifecycle.
	EventTypeElicitationRequested EventType = "elicitation_requested"
	EventTypeElicitationResolved  EventType = "elicitation_resolved"

	// Linked conversation.
	EventTypeLinkedConversationAttached EventType = "linked_conversation_attached"

	// Skill lifecycle.
	EventTypeSkillStarted         EventType = "skill_started"
	EventTypeSkillCompleted       EventType = "skill_completed"
	EventTypeSkillRegistryUpdated EventType = "skill_registry_updated"

	// Intake lifecycle (workspace-intake LLM router).
	//
	// EventTypeIntakeWorkspaceCompleted fires when the workspace-intake LLM
	// call finishes successfully and produced a usable ClassifierResult.
	// Patch payload (consistent shape regardless of action):
	//   - "action"          — "route" | "answer" | "clarify"
	//   - "selectedAgentId" — agent id (only when action="route")
	//   - "answerLen"       — length of the answer text (only when action="answer")
	//   - "questionLen"     — length of the clarification question (only when action="clarify")
	//   - "durationMs"      — wall-clock duration of the LLM call
	//   - "model"           — model id used
	//   - "source"          — always "workspace"
	//
	// EventTypeIntakeWorkspaceFailed fires when the workspace-intake LLM
	// call errors, times out, or returns unparseable output. Patch payload:
	//   - "reason"          — short failure reason (e.g. "llm_error", "parse_error")
	//   - "fallbackAgentId" — agent id the runtime fell back to (when applicable)
	//   - "model"           — model id attempted
	//   - "errMessage"      — trimmed error message (when present)
	EventTypeIntakeWorkspaceCompleted EventType = "intake.workspace.completed"
	EventTypeIntakeWorkspaceFailed    EventType = "intake.workspace.failed"

	// Tool feed lifecycle.
	EventTypeToolFeedActive   EventType = "tool_feed_active"
	EventTypeToolFeedInactive EventType = "tool_feed_inactive"

	// Conversation metadata.
	// Emitted whenever server-side code changes conversation-level fields
	// (title, summary, agentId, …) so clients can update sidebar/header state
	// without polling. Patch contains only the changed fields.
	EventTypeConversationMetaUpdated EventType = "conversation_meta_updated"

	// Stream terminated by the bus because the subscriber's buffer filled
	// up. This is a control event injected by SSE handlers (not a regular
	// bus event) to signal clients that one or more events may have been
	// missed and they should reconnect. The event's EventSeq carries the
	// last successfully delivered sequence so clients know where to
	// resume from (once Phase 2 resume support lands).
	EventTypeStreamOverflow EventType = "stream_overflow"
)

type EventModel struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type PlannedToolCall struct {
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
}

// Event is a transport-neutral streaming event.
type Event struct {
	ID                        string                 `json:"id,omitempty"`
	StreamID                  string                 `json:"streamId,omitempty"`
	ConversationID            string                 `json:"conversationId,omitempty"`
	TurnID                    string                 `json:"turnId,omitempty"`
	MessageID                 string                 `json:"messageId,omitempty"`
	EventSeq                  int64                  `json:"eventSeq,omitempty"`
	AgentIDUsed               string                 `json:"agentIdUsed,omitempty"`
	AgentName                 string                 `json:"agentName,omitempty"`
	AssistantMessageID        string                 `json:"assistantMessageId,omitempty"`
	ParentMessageID           string                 `json:"parentMessageId,omitempty"`
	RequestID                 string                 `json:"requestId,omitempty"`
	ResponseID                string                 `json:"responseId,omitempty"`
	OperationID               string                 `json:"operationId,omitempty"`
	ToolCallID                string                 `json:"toolCallId,omitempty"`
	ToolMessageID             string                 `json:"toolMessageId,omitempty"`
	RequestPayloadID          string                 `json:"requestPayloadId,omitempty"`
	ResponsePayloadID         string                 `json:"responsePayloadId,omitempty"`
	ProviderRequestPayloadID  string                 `json:"providerRequestPayloadId,omitempty"`
	ProviderResponsePayloadID string                 `json:"providerResponsePayloadId,omitempty"`
	StreamPayloadID           string                 `json:"streamPayloadId,omitempty"`
	LinkedConversationID      string                 `json:"linkedConversationId,omitempty"`
	LinkedConversationAgentID string                 `json:"linkedConversationAgentId,omitempty"`
	LinkedConversationTitle   string                 `json:"linkedConversationTitle,omitempty"`
	ExecutionRole             string                 `json:"executionRole,omitempty"`
	Phase                     string                 `json:"phase,omitempty"`
	PageID                    string                 `json:"pageId,omitempty"`
	Mode                      string                 `json:"mode,omitempty"`
	Type                      EventType              `json:"type"`
	Op                        string                 `json:"op,omitempty"`
	Patch                     map[string]interface{} `json:"patch,omitempty"`
	Content                   string                 `json:"content,omitempty"`
	Narration                 string                 `json:"narration,omitempty"`
	// NarrationSource identifies the author of a narration payload.
	// Populated on narration events and on model_completed events that
	// carry an inline Narration field. Values:
	//
	//   - "model"     — emitted by the model as pre-tool-call framing
	//   - "reasoning" — model's thinking / reasoning content
	//   - "narrator"  — emitted by the async runtime narrator pipeline
	//
	// Empty when no narration is present on the event. Clients that want
	// to render reasoning differently from progress commentary (e.g. to
	// hide reasoning, tag it, or group it separately) branch on this
	// field. Tool-agnostic and event-agnostic.
	NarrationSource  string                 `json:"narrationSource,omitempty"`
	ToolName         string                 `json:"toolName,omitempty"`
	SkillName        string                 `json:"skillName,omitempty"`
	SkillExecutionID string                 `json:"skillExecutionId,omitempty"`
	Arguments        map[string]interface{} `json:"arguments,omitempty"`
	Error            string                 `json:"error,omitempty"`
	Status           string                 `json:"status,omitempty"`
	Iteration        int                    `json:"iteration,omitempty"`
	PageIndex        int                    `json:"pageIndex,omitempty"`
	PageCount        int                    `json:"pageCount,omitempty"`
	LatestPage       bool                   `json:"latestPage,omitempty"`
	// FinalResponse is DEPRECATED on streaming events. The "final
	// assistant message" concept has been removed — end-of-turn is
	// signaled by EventTypeTurnCompleted / EventTypeTurnFailed /
	// EventTypeTurnCanceled; individual messages are all equal-rank.
	// The field remains on the wire for transcript-snapshot fidelity
	// (see sdk/api.ExecutionPage.FinalResponse) but MUST NOT be set on
	// live stream emissions. New code sets it only when copying from
	// persisted page state in a transcript-refresh path.
	FinalResponse    bool                   `json:"finalResponse,omitempty"`
	Model            *EventModel            `json:"model,omitempty"`
	ToolCallsPlanned []PlannedToolCall      `json:"toolCallsPlanned,omitempty"`
	CreatedAt        time.Time              `json:"createdAt,omitempty"`
	ElicitationID    string                 `json:"elicitationId,omitempty"`
	ElicitationData  map[string]interface{} `json:"elicitationData,omitempty"`
	CallbackURL      string                 `json:"callbackUrl,omitempty"`
	ResponsePayload  map[string]interface{} `json:"responsePayload,omitempty"`
	CompletedAt      *time.Time             `json:"completedAt,omitempty"`
	StartedAt        *time.Time             `json:"startedAt,omitempty"`
	UserMessageID    string                 `json:"userMessageId,omitempty"`
	// Queue fields — present on turn_queued events.
	QueueSeq           int    `json:"queueSeq,omitempty"`
	StartedByMessageID string `json:"startedByMessageId,omitempty"`
	ModelCallID        string `json:"modelCallId,omitempty"`
	Provider           string `json:"provider,omitempty"`
	ModelName          string `json:"modelName,omitempty"`
	// Tool feed fields.
	FeedID        string      `json:"feedId,omitempty"`
	FeedTitle     string      `json:"feedTitle,omitempty"`
	FeedItemCount int         `json:"feedItemCount,omitempty"`
	FeedData      interface{} `json:"feedData,omitempty"`
	// Conversation usage summary fields.
	UsageInputTokens     int `json:"usageInputTokens,omitempty"`
	UsageOutputTokens    int `json:"usageOutputTokens,omitempty"`
	UsageEmbeddingTokens int `json:"usageEmbeddingTokens,omitempty"`
	UsageTotalTokens     int `json:"usageTotalTokens,omitempty"`
}

func canonicalEventMessageID(event *Event) string {
	if event == nil {
		return ""
	}
	if messageID := strings.TrimSpace(event.MessageID); messageID != "" {
		return messageID
	}
	if messageID := strings.TrimSpace(event.AssistantMessageID); messageID != "" {
		return messageID
	}
	if messageID := strings.TrimSpace(event.ToolMessageID); messageID != "" {
		return messageID
	}
	if messageID := strings.TrimSpace(event.UserMessageID); messageID != "" {
		return messageID
	}
	if strings.EqualFold(strings.TrimSpace(event.Op), "message_patch") {
		return strings.TrimSpace(event.ID)
	}
	return ""
}

// NormalizeIdentity fills canonical transport identity fields without overwriting
// explicit values already set by the caller.
func (e *Event) NormalizeIdentity(fallbackConversationID, fallbackTurnID string) {
	if e == nil {
		return
	}
	if strings.TrimSpace(e.ConversationID) == "" {
		e.ConversationID = strings.TrimSpace(fallbackConversationID)
	}
	if strings.TrimSpace(e.StreamID) == "" {
		e.StreamID = strings.TrimSpace(e.ConversationID)
	}
	if strings.TrimSpace(e.ConversationID) == "" {
		e.ConversationID = strings.TrimSpace(e.StreamID)
	}
	if strings.TrimSpace(e.TurnID) == "" {
		e.TurnID = strings.TrimSpace(fallbackTurnID)
	}
	if strings.TrimSpace(e.MessageID) == "" {
		e.MessageID = canonicalEventMessageID(e)
	}
}

// FromLLMEvent converts an llm stream event to a generic streaming event.
// When the event carries typed Kind fields, those take precedence over
// the legacy Response-based inference.
func FromLLMEvent(streamID string, in llm.StreamEvent) *Event {
	out := &Event{
		StreamID:       streamID,
		ConversationID: streamID,
		CreatedAt:      time.Now(),
	}

	// Propagate stable IDs from typed stream deltas.
	out.ResponseID = strings.TrimSpace(in.ResponseID)
	out.ID = strings.TrimSpace(in.ItemID)
	out.MessageID = strings.TrimSpace(in.ItemID)
	out.ToolCallID = strings.TrimSpace(in.ToolCallID)

	if in.Err != nil {
		out.Type = EventTypeError
		out.Error = in.Err.Error()
		return out
	}

	// Typed delta path — map each provider Kind to a distinct domain event type.
	if in.Kind != "" {
		switch in.Kind {
		case llm.StreamEventTextDelta:
			out.Type = EventTypeTextDelta
			out.Content = in.Delta
		case llm.StreamEventReasoningDelta:
			out.Type = EventTypeReasoningDelta
			out.Content = in.Delta
		case llm.StreamEventToolCallStarted:
			out.Type = EventTypeToolCallStarted
			out.ToolName = in.ToolName
		case llm.StreamEventToolCallDelta:
			out.Type = EventTypeToolCallDelta
			out.ToolName = in.ToolName
			out.Content = in.Delta
		case llm.StreamEventToolCallCompleted:
			out.Type = EventTypeToolCallCompleted
			out.ToolName = in.ToolName
			out.Arguments = in.Arguments
		case llm.StreamEventUsage:
			out.Type = EventTypeUsage
		case llm.StreamEventItemCompleted:
			out.Type = EventTypeItemCompleted
		case llm.StreamEventTurnStarted:
			out.Type = EventTypeTurnStarted
		case llm.StreamEventTurnCompleted:
			out.Type = EventTypeTurnCompleted
			out.Status = in.FinishReason
		case llm.StreamEventError:
			out.Type = EventTypeError
			out.Error = in.Delta
		}
		return out
	}

	// Legacy Response path — map to canonical types.
	if in.Response == nil {
		out.Type = EventTypeTurnCompleted
		return out
	}
	if len(in.Response.Choices) == 0 {
		out.Type = EventTypeTurnCompleted
		return out
	}
	choice := in.Response.Choices[0]
	msg := choice.Message
	if len(msg.ToolCalls) > 0 {
		out.Type = EventTypeToolCallCompleted
		out.ToolName = msg.ToolCalls[0].Name
		out.Arguments = msg.ToolCalls[0].Arguments
		if out.ToolCallID == "" {
			out.ToolCallID = msg.ToolCalls[0].ID
		}
		return out
	}
	if msg.FunctionCall != nil {
		out.Type = EventTypeToolCallCompleted
		out.ToolName = msg.FunctionCall.Name
		if strings.TrimSpace(msg.FunctionCall.Arguments) != "" {
			args := map[string]interface{}{}
			_ = json.Unmarshal([]byte(msg.FunctionCall.Arguments), &args)
			out.Arguments = args
		}
		return out
	}
	content := llm.MessageText(msg)
	if content == "" {
		out.Type = EventTypeTurnCompleted
		return out
	}
	out.Type = EventTypeTextDelta
	out.Content = content
	return out
}
