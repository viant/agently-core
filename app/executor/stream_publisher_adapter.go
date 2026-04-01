package executor

import (
	"context"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

// streamPublisherAdapter bridges modelcall stream deltas into the runtime streaming bus.
type streamPublisherAdapter struct {
	bus streaming.Publisher
}

func newStreamPublisherAdapter(bus streaming.Publisher) modelcallctx.StreamPublisher {
	if bus == nil {
		return nil
	}
	return &streamPublisherAdapter{bus: bus}
}

func (a *streamPublisherAdapter) Publish(ctx context.Context, ev *modelcallctx.StreamEvent) error {
	if a == nil || a.bus == nil || ev == nil {
		return nil
	}
	convID := strings.TrimSpace(ev.ConversationID)
	if convID == "" {
		return nil
	}

	if ev.Event != nil {
		out := ev.Event
		if strings.TrimSpace(out.StreamID) == "" {
			out.StreamID = convID
		}
		if strings.TrimSpace(out.ConversationID) == "" {
			out.ConversationID = convID
		}
		if out.CreatedAt.IsZero() {
			out.CreatedAt = time.Now()
		}
		if err := a.bus.Publish(ctx, out); err != nil {
			return err
		}
		if debugtrace.Enabled() {
			debugtrace.Write("executor", "timeline", map[string]any{
				"type":             string(out.Type),
				"conversationID":   strings.TrimSpace(out.ConversationID),
				"assistantID":      strings.TrimSpace(out.AssistantMessageID),
				"toolCallsPlanned": out.ToolCallsPlanned,
				"createdAt":        out.CreatedAt.UTC().Format(time.RFC3339Nano),
			})
		}
		return nil
	}

	content := ""
	if asMap, ok := ev.Content.(map[string]interface{}); ok {
		if delta, ok := asMap["delta"].(string); ok {
			content = delta
		}
	} else if asString, ok := ev.Content.(string); ok {
		content = asString
	}
	if strings.TrimSpace(content) == "" {
		return nil
	}

	messageID := ""
	if ev.Message != nil {
		messageID = strings.TrimSpace(ev.Message.Id)
	}
	turnMeta, _ := memory.TurnMetaFromContext(ctx)

	out := &streaming.Event{
		ID:                 messageID,
		StreamID:           convID,
		ConversationID:     convID,
		TurnID:             strings.TrimSpace(turnMeta.TurnID),
		AgentIDUsed:        strings.TrimSpace(turnMeta.Assistant),
		UserMessageID:      strings.TrimSpace(turnMeta.ParentMessageID),
		Type:               streaming.EventTypeTextDelta,
		Mode:               strings.TrimSpace(memory.RequestModeFromContext(ctx)),
		AssistantMessageID: messageID,
		ParentMessageID:    strings.TrimSpace(turnMeta.ParentMessageID),
		ModelCallID:        messageID,
		Content:            content,
		CreatedAt:          time.Now(),
	}
	if err := a.bus.Publish(ctx, out); err != nil {
		return err
	}
	if debugtrace.Enabled() {
		debugtrace.Write("executor", "timeline", map[string]any{
			"type":           string(out.Type),
			"conversationID": strings.TrimSpace(out.ConversationID),
			"turnID":         strings.TrimSpace(out.TurnID),
			"assistantID":    strings.TrimSpace(out.AssistantMessageID),
			"mode":           strings.TrimSpace(out.Mode),
			"contentPreview": out.Content,
			"createdAt":      out.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return nil
}
