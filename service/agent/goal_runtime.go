package agent

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	goalruntime "github.com/viant/agently-core/service/goal"
)

func (s *Service) maybeContinueActiveGoal(ctx context.Context, input *QueryInput, output *QueryOutput, turn runtimerequestctx.TurnMeta, status string) {
	if s == nil || s.goalRuntime == nil || s.dataService == nil || s.conversation == nil || input == nil || output == nil {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(status), "succeeded") {
		return
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		return
	}
	queuedCount, err := s.dataService.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: conversationID,
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil {
		return
	}
	action, goal, err := s.goalRuntime.AfterTurn(ctx, &goalruntime.AfterTurnInput{
		ConversationID:        conversationID,
		TurnStatus:            status,
		RequestTime:           input.RequestTime,
		Usage:                 output.Usage,
		TurnRunning:           false,
		QueuedUserTurns:       queuedCount,
		PendingElicitation:    false,
		PendingApproval:       false,
		PendingAsync:          false,
		UsageLimited:          false,
		ConsecutiveNoProgress: 0,
		AutonomousTurnsUsed:   0,
		Continuation:          nil,
	})
	if err != nil {
		return
	}
	if action.Kind != goalruntime.ActionQueueTurn || action.Continuation == nil {
		return
	}
	goalID := ""
	if goal != nil {
		goalID = goal.ID
	}
	_ = s.enqueueGoalContinuation(ctx, input, turn, goalID, action.Continuation)
}

func (s *Service) enqueueGoalContinuation(ctx context.Context, input *QueryInput, turn runtimerequestctx.TurnMeta, goalID string, hint *goalruntime.ContinuationHint) error {
	if s == nil || s.conversation == nil || hint == nil {
		return nil
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		return nil
	}
	turnID := "goal-" + uuid.NewString()
	queueSeq := time.Now().UnixNano()
	now := time.Now()
	preview := strings.TrimSpace(hint.Preview)
	if preview == "" {
		preview = strings.TrimSpace(hint.Payload)
	}
	payload := strings.TrimSpace(hint.Payload)
	if payload == "" {
		payload = preview
	}

	rec := apiconv.NewTurn()
	rec.SetId(turnID)
	rec.SetConversationID(conversationID)
	rec.SetStatus("queued")
	rec.SetQueueSeq(queueSeq)
	rec.SetOrigin("controller")
	if trimmedGoalID := strings.TrimSpace(goalID); trimmedGoalID != "" {
		rec.SetGoalID(trimmedGoalID)
	}
	if reason := strings.TrimSpace(hint.Reason); reason != "" {
		rec.SetStatusReason(reason)
	}
	rec.SetCreatedAt(now)
	rec.SetStartedByMessageID(turnID)
	if input != nil {
		if actor := strings.TrimSpace(input.Actor()); actor != "" {
			rec.SetAgentIDUsed(actor)
		}
		if model := strings.TrimSpace(input.ModelOverride); model != "" {
			rec.SetModelOverride(model)
		}
	}
	if err := s.conversation.PatchTurn(ctx, rec); err != nil {
		return err
	}

	msg := apiconv.NewMessage()
	msg.SetId(turnID)
	msg.SetConversationID(conversationID)
	msg.SetTurnID(turnID)
	msg.SetRole("user")
	msg.SetType("task")
	msg.SetContent(preview)
	msg.SetRawContent(payload)
	msg.SetCreatedAt(now)
	if input != nil {
		if userID := strings.TrimSpace(input.UserId); userID != "" {
			msg.SetCreatedByUserID(userID)
		}
	}
	if err := s.conversation.PatchMessage(ctx, msg); err != nil {
		return err
	}
	if patcher, ok := s.dataService.(interface {
		PatchTurnQueue(ctx context.Context, in *turnqueuewrite.TurnQueue) error
	}); ok {
		q := &turnqueuewrite.TurnQueue{Has: &turnqueuewrite.TurnQueueHas{}}
		q.SetId(turnID)
		q.SetConversationId(conversationID)
		q.SetTurnId(turnID)
		q.SetMessageId(turnID)
		q.SetQueueSeq(queueSeq)
		q.SetStatus("queued")
		q.SetCreatedAt(now)
		q.SetUpdatedAt(now)
		if err := patcher.PatchTurnQueue(ctx, q); err != nil {
			return err
		}
	}
	s.emitTurnQueued(ctx, conversationID, turnID, queueSeq, now, payload, "controller", goalID, strings.TrimSpace(hint.Reason))
	s.triggerQueueDrain(conversationID)
	return nil
}
