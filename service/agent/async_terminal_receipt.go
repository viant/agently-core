package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/shared/toolexec"
)

// Agent-level reconciliation runs outside core.Generate's model-stream context.
// Publish through the same service bus used for other agent lifecycle events.
func (s *Service) publishTerminalCarrierReceipt(ctx context.Context, turn runtimerequestctx.TurnMeta, rec *asynccfg.OperationRecord) error {
	if s == nil || s.streamPub == nil || rec == nil {
		return nil
	}
	var eventType streaming.EventType
	switch rec.State {
	case asynccfg.StateCompleted:
		eventType = streaming.EventTypeToolCallCompleted
	case asynccfg.StateFailed:
		eventType = streaming.EventTypeToolCallFailed
	case asynccfg.StateCanceled:
		eventType = streaming.EventTypeToolCallCanceled
	default:
		return nil
	}
	if strings.TrimSpace(rec.ToolCallID) == "" || strings.TrimSpace(rec.ToolMessageID) == "" {
		return fmt.Errorf("terminal async carrier identity is incomplete")
	}
	return s.streamPub.Publish(ctx, &streaming.Event{
		Type: eventType, ConversationID: turn.ConversationID, StreamID: turn.ConversationID, TurnID: turn.TurnID,
		ToolCallID: rec.ToolCallID, ToolMessageID: rec.ToolMessageID, MessageID: rec.ToolMessageID,
		ToolName: rec.ToolName, OperationID: rec.ID, Status: rec.Status, Error: rec.Error,
		Content: toolexec.AsyncPersistenceContentFromRecord(rec), CreatedAt: time.Now(),
	})
}
