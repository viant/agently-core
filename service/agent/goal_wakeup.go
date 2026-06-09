package agent

import (
	"context"
	"strings"
	"time"

	aggoal "github.com/viant/agently-core/pkg/agently/goal"
	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	goalruntime "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/workspace"
	wscfg "github.com/viant/agently-core/workspace/config"
)

const autonomousGoalWakeupContextKey = "autonomous.goalWakeup"

func AutonomousGoalWakeupContextKey() string {
	return autonomousGoalWakeupContextKey
}

type GoalWakeupRequest struct {
	ConversationID string
	GoalID         string
	UserID         string
	AgentID        string
	ModelOverride  string
	WakeAt         time.Time
	Preview        string
	Payload        string
}

type goalWakeupScheduler interface {
	ScheduleGoalWakeup(ctx context.Context, req GoalWakeupRequest) (bool, error)
	CancelGoalWakeups(ctx context.Context, conversationID, goalID string) error
}

func goalWakeupGoalID(input *QueryInput) string {
	if input == nil || input.Context == nil {
		return ""
	}
	raw, ok := input.Context[autonomousGoalWakeupContextKey]
	if !ok || raw == nil {
		return ""
	}
	switch actual := raw.(type) {
	case string:
		return strings.TrimSpace(actual)
	case map[string]any:
		if value, ok := actual["goalId"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Service) cancelPendingGoalWakeups(ctx context.Context, conversationID string) {
	if s == nil || s.goalWakeups == nil {
		return
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	_ = s.goalWakeups.CancelGoalWakeups(context.WithoutCancel(ctx), conversationID, "")
}

func (s *Service) shouldSkipAutonomousGoalWakeup(ctx context.Context, input *QueryInput) bool {
	if s == nil || s.dataService == nil || input == nil {
		return false
	}
	goalID := goalWakeupGoalID(input)
	conversationID := strings.TrimSpace(input.ConversationID)
	if goalID == "" || conversationID == "" {
		return false
	}
	current, err := s.dataService.GetGoal(ctx, conversationID, &aggoal.GoalInput{
		ConversationID: conversationID,
		Has:            &aggoal.GoalInputHas{ConversationID: true},
	})
	if err != nil || current == nil {
		return true
	}
	if strings.TrimSpace(current.Id) != goalID || !strings.EqualFold(strings.TrimSpace(current.Status), "active") {
		return true
	}
	activeTurn, err := s.dataService.GetActiveTurn(ctx, &agturnactive.ActiveTurnsInput{
		ConversationID: conversationID,
		Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
	})
	if err != nil {
		return true
	}
	if activeTurn != nil {
		return true
	}
	queuedCount, err := s.dataService.CountQueuedTurns(ctx, &agturncount.QueuedTotalInput{
		ConversationID: conversationID,
		Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
	})
	if err != nil || queuedCount > 0 {
		return true
	}
	signals := gatherControllerSignals(ctx, s.dataService, s.asyncManager, conversationID)
	if signals.PendingApproval || signals.PendingAsync || signals.PendingElicitation {
		return true
	}
	return s.hasQueuedControllerTurnForGoal(ctx, conversationID, goalID)
}

func (s *Service) scheduleGoalWakeup(ctx context.Context, input *QueryInput, conversationID, goalID string, hint *goalruntime.ContinuationHint, delaySeconds int) error {
	if s == nil || hint == nil || delaySeconds <= 0 {
		return nil
	}
	effectiveDelay, enabled := resolveGoalWakeupDelay(delaySeconds)
	if !enabled {
		return s.enqueueGoalContinuation(ctx, input, runtimerequestctx.TurnMeta{ConversationID: conversationID}, goalID, hint)
	}
	if s.goalWakeups == nil {
		return s.enqueueGoalContinuation(ctx, input, runtimerequestctx.TurnMeta{ConversationID: conversationID}, goalID, hint)
	}
	req := GoalWakeupRequest{
		ConversationID: strings.TrimSpace(conversationID),
		GoalID:         strings.TrimSpace(goalID),
		WakeAt:         time.Now().UTC().Add(time.Duration(effectiveDelay) * time.Second),
		Preview:        strings.TrimSpace(hint.Preview),
		Payload:        strings.TrimSpace(hint.Payload),
	}
	if input != nil {
		req.UserID = strings.TrimSpace(input.UserId)
		req.AgentID = strings.TrimSpace(input.Actor())
		req.ModelOverride = strings.TrimSpace(input.ModelOverride)
	}
	if req.AgentID == "" || req.UserID == "" || req.ModelOverride == "" {
		conv, err := s.conversation.GetConversation(context.WithoutCancel(ctx), req.ConversationID)
		if err == nil && conv != nil {
			if req.AgentID == "" && conv.AgentId != nil {
				req.AgentID = strings.TrimSpace(*conv.AgentId)
			}
			if req.UserID == "" && conv.CreatedByUserId != nil {
				req.UserID = strings.TrimSpace(*conv.CreatedByUserId)
			}
			if req.ModelOverride == "" && conv.DefaultModel != nil {
				req.ModelOverride = strings.TrimSpace(*conv.DefaultModel)
			}
		}
	}
	if req.AgentID == "" {
		return s.enqueueGoalContinuation(ctx, input, runtimerequestctx.TurnMeta{ConversationID: conversationID}, goalID, hint)
	}
	scheduled, err := s.goalWakeups.ScheduleGoalWakeup(context.WithoutCancel(ctx), req)
	if err != nil {
		return err
	}
	if !scheduled {
		return s.enqueueGoalContinuation(ctx, input, runtimerequestctx.TurnMeta{ConversationID: conversationID}, goalID, hint)
	}
	s.emitGoalControllerScheduled(ctx, conversationID, "", goalID, "wakeup", &req.WakeAt, hint)
	return nil
}

func resolveGoalWakeupDelay(requestedSeconds int) (int, bool) {
	delay := requestedSeconds
	if delay <= 0 {
		return 0, false
	}
	cfg, err := wscfg.Load(workspace.Root())
	if err == nil && cfg != nil {
		if !cfg.GoalWakeupsEnabled() {
			return 0, false
		}
		minDelay := cfg.GoalWakeupMinDelaySeconds()
		maxDelay := cfg.GoalWakeupMaxDelaySeconds()
		if minDelay > 0 && delay < minDelay {
			delay = minDelay
		}
		if maxDelay > 0 && delay > maxDelay {
			delay = maxDelay
		}
		return delay, true
	}
	if delay < 60 {
		delay = 60
	}
	if delay > 3600 {
		delay = 3600
	}
	return delay, true
}
