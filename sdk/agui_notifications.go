package sdk

import (
	"context"
	"log"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
)

// A hint carries no execution payload or private journal identity. Consumers
// reauthorize bootstrap/attachment after the admission or terminal commit.
func notifyAGUIRunUpdated(ctx context.Context, client Client, threadID string) {
	if notifier, ok := client.(interface{ aguiNotifyRunUpdated(context.Context, string) }); ok {
		notifier.aguiNotifyRunUpdated(ctx, threadID)
	}
}

func (c *backendClient) aguiNotifyRunUpdated(ctx context.Context, threadID string) {
	if c == nil || c.streaming == nil || threadID == "" {
		return
	}
	if err := c.streaming.Publish(context.WithoutCancel(ctx), &streaming.Event{Type: streaming.EventTypeConversationMetaUpdated,
		ConversationID: threadID, StreamID: threadID, CreatedAt: time.Now().UTC(), Patch: map[string]any{"aguiUpdated": true}}); err != nil {
		log.Printf("AG-UI run post-commit notification: %v", err)
	}
}
