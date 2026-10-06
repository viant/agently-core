package agui

import (
	"context"
	"fmt"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"time"
)

// PublishStandard is an explicit runtime bridge for a true standard event.
// Its parent conversation and turn come from trusted execution context.
func PublishStandard(ctx context.Context, publisher streaming.Publisher, event *WireEvent) error {
	if publisher == nil {
		return fmt.Errorf("standard event publisher is required")
	}
	turn, ok := requestctx.TurnMetaFromContext(ctx)
	if !ok || turn.ConversationID == "" || turn.TurnID == "" {
		return fmt.Errorf("standard event publication requires active turn identity")
	}
	raw, err := EncodeEvent(event)
	if err != nil {
		return err
	}
	if reservedInvocationEvent(raw) {
		return fmt.Errorf("native invocation ancestry belongs to trusted registration")
	}
	return publisher.Publish(ctx, &streaming.Event{Type: streaming.EventTypeProtocol, ConversationID: turn.ConversationID, StreamID: turn.ConversationID, TurnID: turn.TurnID, ProtocolEvent: raw, CreatedAt: time.Now()})
}
