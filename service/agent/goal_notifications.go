package agent

import (
	"context"
	"time"

	"github.com/viant/agently-core/runtime/streaming"
	goalsys "github.com/viant/agently-core/service/goal"
)

// emitGoalAfterAccounting invalidates goal subscribers only after durable usage
// and controller transitions have succeeded. Consumers fetch authoritative state.
func (s *Service) emitGoalAfterAccounting(ctx context.Context, conversationID, turnID string, goal *goalsys.Goal) {
	if s == nil || s.streamPub == nil || goal == nil {
		return
	}
	event := &streaming.Event{Type: streaming.EventTypeGoalUpdated, ConversationID: conversationID, StreamID: conversationID, TurnID: turnID, GoalID: goal.ID, Status: string(goal.Status), CreatedAt: time.Now().UTC()}
	event.NormalizeIdentity(conversationID, turnID)
	_ = s.streamPub.Publish(ctx, event)
}
