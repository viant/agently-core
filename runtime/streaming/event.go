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

	// Model lifecycle.
	EventTypeModelStarted   EventType = "model_started"
	EventTypeModelCompleted EventType = "model_completed"

	// Assistant content (aggregated).
	EventTypeAssistantPreamble EventType = "assistant_preamble"
	EventTypeAssistantFinal    EventType = "assistant_final"

	// Tool call lifecycle.
	EventTypeToolCallsPlanned  EventType = "tool_calls_planned"
	EventTypeToolCallStarted   EventType = "tool_call_started"
	EventTypeToolCallCompleted EventType = "tool_call_completed"

	// Stream metadata / completion.
	EventTypeItemCompleted EventType = "item_completed"
	EventTypeUsage         EventType = "usage"

	// Elicitation lifecycle.
	EventTypeElicitationRequested EventType = "elicitation_requested"
	EventTypeElicitationResolved  EventType = "elicitation_resolved"

	// Linked conversation.
	EventTypeLinkedConversationAttached EventType = "linked_conversation_attached"
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
	ID                   string                 `json:"id,omitempty"`
	StreamID             string                 `json:"streamId,omitempty"`
	ConversationID       string                 `json:"conversationId,omitempty"`
	TurnID               string                 `json:"turnId,omitempty"`
	AssistantMessageID   string                 `json:"assistantMessageId,omitempty"`
	ParentMessageID      string                 `json:"parentMessageId,omitempty"`
	RequestID            string                 `json:"requestId,omitempty"`
	ResponseID           string                 `json:"responseId,omitempty"`
	ToolCallID           string                 `json:"toolCallId,omitempty"`
	ToolMessageID        string                 `json:"toolMessageId,omitempty"`
	RequestPayloadID     string                 `json:"requestPayloadId,omitempty"`
	ResponsePayloadID    string                 `json:"responsePayloadId,omitempty"`
	LinkedConversationID string                 `json:"linkedConversationId,omitempty"`
	Type                 EventType              `json:"type"`
	Op                   string                 `json:"op,omitempty"`
	Patch                map[string]interface{} `json:"patch,omitempty"`
	Content              string                 `json:"content,omitempty"`
	Preamble             string                 `json:"preamble,omitempty"`
	ToolName             string                 `json:"toolName,omitempty"`
	Arguments            map[string]interface{} `json:"arguments,omitempty"`
	Error                string                 `json:"error,omitempty"`
	Status               string                 `json:"status,omitempty"`
	Iteration            int                    `json:"iteration,omitempty"`
	PageIndex            int                    `json:"pageIndex,omitempty"`
	PageCount            int                    `json:"pageCount,omitempty"`
	LatestPage           bool                   `json:"latestPage,omitempty"`
	FinalResponse        bool                   `json:"finalResponse,omitempty"`
	Model                *EventModel            `json:"model,omitempty"`
	ToolCallsPlanned     []PlannedToolCall      `json:"toolCallsPlanned,omitempty"`
	CreatedAt            time.Time              `json:"createdAt,omitempty"`
	ElicitationID        string                 `json:"elicitationId,omitempty"`
	ElicitationData      map[string]interface{} `json:"elicitationData,omitempty"`
	CallbackURL          string                 `json:"callbackUrl,omitempty"`
	ResponsePayload      map[string]interface{} `json:"responsePayload,omitempty"`
	CompletedAt          *time.Time             `json:"completedAt,omitempty"`
	StartedAt            *time.Time             `json:"startedAt,omitempty"`
	UserMessageID        string                 `json:"userMessageId,omitempty"`
	ModelCallID          string                 `json:"modelCallId,omitempty"`
	Provider             string                 `json:"provider,omitempty"`
	ModelName            string                 `json:"modelName,omitempty"`
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
