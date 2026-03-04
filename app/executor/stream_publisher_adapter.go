package executor

import (
	"context"
	"strings"
	"time"

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

	out := &streaming.Event{
		ID:        messageID,
		StreamID:  convID,
		Type:      streaming.EventTypeChunk,
		Content:   content,
		CreatedAt: time.Now(),
	}
	return a.bus.Publish(ctx, out)
}
