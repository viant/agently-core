package memory

import (
	"context"
	"sync"
)

// ModelMessageIDKey carries the message id to which the current model call should attach.
type modelMessageIDKey string

var ModelMessageIDKey = modelMessageIDKey("modelMessageID")

func ModelMessageIDFromContext(ctx context.Context) string {
	value := ctx.Value(ModelMessageIDKey)
	if value == nil {
		return ""
	}
	return value.(string)
}

// ToolMessageIDKey carries the message id to which the current tool call should attach.
type toolMessageIDKey string

var ToolMessageIDKey = toolMessageIDKey("toolMessageID")

func WithToolMessageID(ctx context.Context, messageID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if messageID == "" {
		return ctx
	}
	return context.WithValue(ctx, ToolMessageIDKey, messageID)
}

func ToolMessageIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value := ctx.Value(ToolMessageIDKey)
	if value == nil {
		return ""
	}
	if id, ok := value.(string); ok {
		return id
	}
	return ""
}

// TurnMeta captures minimal per-turn context for downstream persistence.
// Prefer passing a single TurnMeta instead of scattering separate keys.
type TurnMeta struct {
	TurnID          string
	Assistant       string
	ConversationID  string
	ParentMessageID string // last user message id (or tool message when parenting final)
}

type turnMetaKeyT string

var turnMetaKey = turnMetaKeyT("turnMeta")

// turnTrace holds a per-turn provider trace/anchor id (e.g., OpenAI response.id)
var turnTrace sync.Map // key: turnID string -> value: traceID string

// SetTurnTrace stores a provider trace/anchor id for the given turn id.
func SetTurnTrace(turnID, traceID string) {
	if turnID == "" || traceID == "" {
		return
	}
	turnTrace.Store(turnID, traceID)
}

// TurnTrace returns a previously stored provider trace/anchor id for this turn.
func TurnTrace(turnID string) string {
	if turnID == "" {
		return ""
	}
	if v, ok := turnTrace.Load(turnID); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

// turnModelMsgID holds the per-turn assistant message UUID set by OnCallStart.
// This bridges the gap between the observer (which creates the ID) and the
// stream handler (which needs it for parent_message_id on tool_op messages).
var turnModelMsgID sync.Map // key: turnID string -> value: msgID string

// SetTurnModelMessageID stores the current model call's assistant message ID.
func SetTurnModelMessageID(turnID, msgID string) {
	if turnID == "" || msgID == "" {
		return
	}
	turnModelMsgID.Store(turnID, msgID)
}

// TurnModelMessageID returns the most recent assistant message ID for this turn.
func TurnModelMessageID(turnID string) string {
	if turnID == "" {
		return ""
	}
	if v, ok := turnModelMsgID.Load(turnID); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}

// CleanupTurn removes all in-memory state for the given turn.
// Call this when the turn completes or is canceled to prevent memory leaks.
func CleanupTurn(turnID string) {
	if turnID == "" {
		return
	}
	turnTrace.Delete(turnID)
	turnModelMsgID.Delete(turnID)
}

// WithTurnMeta stores TurnMeta on the context and also seeds individual keys
// for backward compatibility with existing readers.
func WithTurnMeta(ctx context.Context, meta TurnMeta) context.Context {

	if meta.ConversationID != "" {
		ctx = context.WithValue(ctx, ConversationIDKey, meta.ConversationID)
	}
	return context.WithValue(ctx, turnMetaKey, meta)
}

// TurnMetaFromContext returns a stored TurnMeta when present.
func TurnMetaFromContext(ctx context.Context) (TurnMeta, bool) {
	if ctx == nil {
		return TurnMeta{}, false
	}
	if v := ctx.Value(turnMetaKey); v != nil {
		if m, ok := v.(TurnMeta); ok {
			return m, true
		}
	}
	return TurnMeta{}, false
}

// ModelCompletionMeta carries LLM response data through context so that
// emitCanonicalModelEvent can include all data points in a single
// model_completed event (content, preamble, finalResponse, finish reason).
type ModelCompletionMeta struct {
	Content       string
	Preamble      string
	FinalResponse bool
	FinishReason  string
}

type modelCompletionMetaKeyT string

var modelCompletionMetaKey = modelCompletionMetaKeyT("modelCompletionMeta")

// WithModelCompletionMeta stores completion metadata on the context.
func WithModelCompletionMeta(ctx context.Context, meta ModelCompletionMeta) context.Context {
	return context.WithValue(ctx, modelCompletionMetaKey, meta)
}

// ModelCompletionMetaFromContext returns completion metadata if present.
func ModelCompletionMetaFromContext(ctx context.Context) (ModelCompletionMeta, bool) {
	if ctx == nil {
		return ModelCompletionMeta{}, false
	}
	if v := ctx.Value(modelCompletionMetaKey); v != nil {
		if m, ok := v.(ModelCompletionMeta); ok {
			return m, true
		}
	}
	return ModelCompletionMeta{}, false
}

// EmbedFunc defines a function that creates embeddings for given texts.
// It should return one embedding per input text.
type EmbedFunc func(ctx context.Context, texts []string) ([][]float32, error)
