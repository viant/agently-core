package streaming

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
)

type EventType string

const (
	EventTypeChunk   EventType = "chunk"
	EventTypeTool    EventType = "tool"
	EventTypeDone    EventType = "done"
	EventTypeError   EventType = "error"
	EventTypeControl EventType = "control"
)

// Event is a transport-neutral streaming event.
type Event struct {
	ID        string                 `json:"id,omitempty"`
	StreamID  string                 `json:"streamId,omitempty"`
	Type      EventType              `json:"type"`
	Op        string                 `json:"op,omitempty"`
	Patch     map[string]interface{} `json:"patch,omitempty"`
	Content   string                 `json:"content,omitempty"`
	ToolName  string                 `json:"toolName,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Error     string                 `json:"error,omitempty"`
	CreatedAt time.Time              `json:"createdAt,omitempty"`
}

// FromLLMEvent converts an llm stream event to a generic streaming event.
func FromLLMEvent(streamID string, in llm.StreamEvent) *Event {
	out := &Event{
		StreamID:  streamID,
		CreatedAt: time.Now(),
	}
	if in.Err != nil {
		out.Type = EventTypeError
		out.Error = in.Err.Error()
		return out
	}
	if in.Response == nil {
		out.Type = EventTypeDone
		return out
	}
	if len(in.Response.Choices) == 0 {
		out.Type = EventTypeDone
		return out
	}
	choice := in.Response.Choices[0]
	msg := choice.Message
	if len(msg.ToolCalls) > 0 {
		out.Type = EventTypeTool
		out.ToolName = msg.ToolCalls[0].Name
		out.Arguments = msg.ToolCalls[0].Arguments
		return out
	}
	if msg.FunctionCall != nil {
		out.Type = EventTypeTool
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
		out.Type = EventTypeDone
		return out
	}
	out.Type = EventTypeChunk
	out.Content = content
	return out
}
