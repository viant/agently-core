package llm

import (
	"context"
)

// StreamEventKind classifies the semantic type of a stream delta.
type StreamEventKind string

const (
	StreamEventTurnStarted       StreamEventKind = "turn_started"
	StreamEventTextDelta         StreamEventKind = "text_delta"
	StreamEventReasoningDelta    StreamEventKind = "reasoning_delta"
	StreamEventToolCallStarted   StreamEventKind = "tool_call_started"
	StreamEventToolCallDelta     StreamEventKind = "tool_call_delta"
	StreamEventToolCallCompleted StreamEventKind = "tool_call_completed"
	StreamEventUsage             StreamEventKind = "usage"
	StreamEventItemCompleted     StreamEventKind = "item_completed"
	StreamEventTurnCompleted     StreamEventKind = "turn_completed"
	StreamEventError             StreamEventKind = "error"
)

// StreamEvent represents a partial or complete event in a streaming LLM response.
//
// Providers should populate Kind and the typed delta fields. Providers that
// have not yet been migrated to typed deltas may still set Response directly.
type StreamEvent struct {
	// Kind classifies this delta. When empty, consumers fall back to Response.
	Kind StreamEventKind

	// Identity — stable IDs assigned by the provider adapter.
	ResponseID string // Provider response/session ID (e.g. OpenAI resp_xxx).
	ItemID     string // Stable ID for the item this delta belongs to.
	ToolCallID string // Stable tool-call ID for tool_call_* events.

	// Delta payloads — only the relevant field is populated per Kind.
	Role      MessageRole            // Set on turn_started or first delta.
	Delta     string                 // Text or reasoning delta content.
	ToolName  string                 // Tool name for tool_call_started.
	Arguments map[string]interface{} // Completed arguments for tool_call_completed.

	// Metadata
	Usage        *Usage // Token usage snapshot (usage events or final).
	FinishReason string // Provider finish reason on turn_completed.

	// Response carries the full GenerateResponse snapshot. Used by providers
	// not yet migrated to typed Kind deltas.
	Response *GenerateResponse

	// Err indicates a streaming error.
	Err error
}

// StreamingModel is an optional interface for LLM providers that support streaming responses.
type StreamingModel interface {
	// Stream sends a chat request with streaming enabled and returns a channel of StreamEvent.
	Stream(ctx context.Context, request *GenerateRequest) (<-chan StreamEvent, error)
}
