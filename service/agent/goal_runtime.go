package agent

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	turnqueuewrite "github.com/viant/agently-core/pkg/agently/turnqueue/write"
	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
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
	signals := gatherControllerSignals(ctx, s.dataService, s.asyncManager, conversationID)
	continuation := buildGoalContinuationHint(output)
	action, goal, err := s.goalRuntime.AfterTurn(ctx, &goalruntime.AfterTurnInput{
		ConversationID:        conversationID,
		TurnStatus:            status,
		RequestTime:           input.RequestTime,
		Usage:                 output.Usage,
		TurnRunning:           false,
		QueuedUserTurns:       queuedCount,
		PendingElicitation:    signals.PendingElicitation,
		PendingApproval:       signals.PendingApproval,
		PendingAsync:          signals.PendingAsync,
		UsageLimited:          false,
		ConsecutiveNoProgress: 0,
		AutonomousTurnsUsed:   signals.AutonomousTurnsUsed,
		Continuation:          continuation,
		ProgressFingerprint:   buildGoalProgressFingerprint(output, continuation),
	})
	if err != nil {
		return
	}
	if action.Continuation == nil {
		return
	}
	goalID := ""
	if goal != nil {
		goalID = goal.ID
	}
	switch action.Kind {
	case goalruntime.ActionQueueTurn:
		_ = s.enqueueGoalContinuation(ctx, input, turn, goalID, action.Continuation)
	case goalruntime.ActionScheduleWakeup:
		_ = s.scheduleGoalWakeup(ctx, input, conversationID, goalID, action.Continuation, action.WakeDelaySeconds)
	}
}

// controllerSignals holds the truthful, durably-sourced controller snapshot
// inputs gathered after a turn completes.
type controllerSignals struct {
	PendingElicitation  bool
	PendingApproval     bool
	PendingAsync        bool
	AutonomousTurnsUsed int
}

// controllerSignalCounter is the narrow set of count methods the concrete data
// service exposes for controller snapshot inputs. It is intentionally narrower
// than data.Service so the methods can be added to the concrete implementation
// without widening the shared interface (mirrors the PatchTurnQueue assertion).
type controllerSignalCounter interface {
	CountControllerTurns(ctx context.Context, conversationID string, opts ...data.Option) (int, error)
	CountPendingApprovals(ctx context.Context, conversationID string, opts ...data.Option) (int, error)
	CountPendingElicitations(ctx context.Context, conversationID string, opts ...data.Option) (int, error)
}

// gatherControllerSignals derives truthful controller snapshot inputs from the
// durable store. Each signal degrades safely: a missing capability or a query
// error yields the conservative zero value, which keeps the controller from
// continuing on stale/unknown state rather than blocking the turn.
func gatherControllerSignals(ctx context.Context, dataService interface{}, asyncManager *asynccfg.Manager, conversationID string) controllerSignals {
	signals := controllerSignals{}
	counter, ok := dataService.(controllerSignalCounter)
	if ok {
		if n, err := counter.CountPendingElicitations(ctx, conversationID); err == nil {
			signals.PendingElicitation = n > 0
		}
		if n, err := counter.CountPendingApprovals(ctx, conversationID); err == nil {
			signals.PendingApproval = n > 0
		}
		if n, err := counter.CountControllerTurns(ctx, conversationID); err == nil && n > 0 {
			signals.AutonomousTurnsUsed = n
		}
	}
	if asyncManager != nil {
		signals.PendingAsync = len(asyncManager.ListOperations(asynccfg.Filter{
			ConversationID: conversationID,
		})) > 0
	}
	return signals
}

func (s *Service) enqueueGoalContinuation(ctx context.Context, input *QueryInput, turn runtimerequestctx.TurnMeta, goalID string, hint *goalruntime.ContinuationHint) error {
	if s == nil || s.conversation == nil || hint == nil {
		return nil
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	if conversationID == "" {
		return nil
	}
	if strings.TrimSpace(goalID) != "" && s.hasQueuedControllerTurnForGoal(ctx, conversationID, goalID) {
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
	s.emitGoalControllerScheduled(ctx, conversationID, turnID, goalID, "queue", nil, hint)
	s.triggerQueueDrain(conversationID)
	return nil
}

func (s *Service) hasQueuedControllerTurnForGoal(ctx context.Context, conversationID, goalID string) bool {
	if s == nil || s.conversation == nil {
		return false
	}
	conversationID = strings.TrimSpace(conversationID)
	goalID = strings.TrimSpace(goalID)
	if conversationID == "" || goalID == "" {
		return false
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return false
	}
	for _, item := range conv.GetTranscript() {
		if item == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Status), "queued") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(valueOrEmpty(item.Origin)), "controller") {
			continue
		}
		if strings.TrimSpace(valueOrEmpty(item.GoalID)) != goalID {
			continue
		}
		return true
	}
	return false
}

func (s *Service) emitGoalControllerScheduled(ctx context.Context, conversationID, turnID, goalID, mode string, wakeAt *time.Time, hint *goalruntime.ContinuationHint) {
	if s == nil || s.streamPub == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(goalID) == "" || hint == nil {
		return
	}
	status := "queued"
	if strings.EqualFold(strings.TrimSpace(mode), "wakeup") {
		status = "scheduled"
	}
	patch := map[string]interface{}{
		"reason":  strings.TrimSpace(hint.Reason),
		"preview": strings.TrimSpace(hint.Preview),
		"payload": strings.TrimSpace(hint.Payload),
		"mode":    strings.TrimSpace(mode),
	}
	if wakeAt != nil && !wakeAt.IsZero() {
		patch["wakeAt"] = wakeAt.UTC().Format(time.RFC3339Nano)
	}
	ev := &streaming.Event{
		Type:           streaming.EventTypeGoalControllerScheduled,
		ConversationID: strings.TrimSpace(conversationID),
		StreamID:       strings.TrimSpace(conversationID),
		TurnID:         strings.TrimSpace(turnID),
		MessageID:      strings.TrimSpace(turnID),
		GoalID:         strings.TrimSpace(goalID),
		Status:         status,
		StatusReason:   strings.TrimSpace(hint.Reason),
		Content:        strings.TrimSpace(hint.Preview),
		Patch:          patch,
		CreatedAt:      time.Now(),
	}
	ev.NormalizeIdentity(strings.TrimSpace(conversationID), strings.TrimSpace(turnID))
	_ = s.streamPub.Publish(ctx, ev)
}
