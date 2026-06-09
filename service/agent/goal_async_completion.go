package agent

import (
	"context"
	"strings"

	agturnactive "github.com/viant/agently-core/pkg/agently/turn/active"
	agturncount "github.com/viant/agently-core/pkg/agently/turn/queuedCount"
	asynccfg "github.com/viant/agently-core/protocol/async"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	goalsys "github.com/viant/agently-core/service/goal"
)

var asyncGoalCompletionGuards = &convGuardMap{m: make(map[string]*int32)}

func (s *Service) observeDetachedAsyncGoalCompletion(ctx context.Context, rec *asynccfg.OperationRecord) {
	if s == nil || s.asyncManager == nil || s.goalRuntime == nil || s.dataService == nil || s.conversation == nil || rec == nil {
		return
	}
	if asynccfg.ExecutionModeWaits(rec.ExecutionMode) {
		return
	}
	opID := strings.TrimSpace(rec.ID)
	conversationID := strings.TrimSpace(rec.ParentConvID)
	if opID == "" || conversationID == "" {
		return
	}
	if _, loaded := s.asyncGoalWatches.LoadOrStore(opID, struct{}{}); loaded {
		return
	}
	go func(seed *asynccfg.OperationRecord) {
		defer s.asyncGoalWatches.Delete(opID)
		resultCh := s.asyncManager.AwaitTerminal(context.Background(), []string{opID})
		result, ok := <-resultCh
		if !ok || result.OpsStillActive {
			return
		}
		if !asyncGoalCompletionGuards.acquire(conversationID) {
			return
		}
		defer asyncGoalCompletionGuards.release(conversationID)
		latest, found := s.asyncManager.Get(context.Background(), opID)
		if !found || latest == nil || latest.State != asynccfg.StateCompleted {
			return
		}
		if s.currentGoalAsyncPolicy(context.Background(), conversationID) == goalsys.AsyncPolicyWait {
			return
		}
		active, err := s.dataService.GetActiveTurn(context.Background(), &agturnactive.ActiveTurnsInput{
			ConversationID: conversationID,
			Has:            &agturnactive.ActiveTurnsInputHas{ConversationID: true},
		})
		if err != nil || active != nil {
			return
		}
		queuedCount, err := s.dataService.CountQueuedTurns(context.Background(), &agturncount.QueuedTotalInput{
			ConversationID: conversationID,
			Has:            &agturncount.QueuedTotalInputHas{ConversationID: true},
		})
		if err != nil {
			return
		}
		signals := gatherControllerSignals(context.Background(), s.dataService, s.asyncManager, conversationID)
		asyncOutput := asyncCompletionQueryOutput(latest)
		continuation := buildGoalContinuationHint(asyncOutput)
		action, goal, err := s.goalRuntime.AfterTurn(context.Background(), &goalsys.AfterTurnInput{
			ConversationID:        conversationID,
			TurnStatus:            "succeeded",
			TurnRunning:           false,
			QueuedUserTurns:       queuedCount,
			PendingElicitation:    signals.PendingElicitation,
			PendingApproval:       signals.PendingApproval,
			PendingAsync:          signals.PendingAsync,
			UsageLimited:          false,
			ConsecutiveNoProgress: 0,
			AutonomousTurnsUsed:   signals.AutonomousTurnsUsed,
			Continuation:          continuation,
			ProgressFingerprint:   buildGoalProgressFingerprint(asyncOutput, continuation),
		})
		if err != nil || action.Continuation == nil {
			return
		}
		goalID := ""
		if goal != nil {
			goalID = goal.ID
		}
		switch action.Kind {
		case goalsys.ActionQueueTurn:
			_ = s.enqueueGoalContinuation(
				context.Background(),
				nil,
				runtimerequestctx.TurnMeta{
					ConversationID:  conversationID,
					TurnID:          strings.TrimSpace(seed.ParentTurnID),
					ParentMessageID: strings.TrimSpace(seed.ToolMessageID),
				},
				goalID,
				action.Continuation,
			)
		case goalsys.ActionScheduleWakeup:
			_ = s.scheduleGoalWakeup(context.Background(), nil, conversationID, goalID, action.Continuation, action.WakeDelaySeconds)
		}
	}(cloneAsyncRecord(rec))
}

func asyncCompletionQueryOutput(rec *asynccfg.OperationRecord) *QueryOutput {
	if rec == nil {
		return &QueryOutput{}
	}
	content := strings.TrimSpace(string(rec.KeyData))
	if content == "" {
		content = strings.TrimSpace(rec.Message)
	}
	return &QueryOutput{Content: content}
}

func cloneAsyncRecord(rec *asynccfg.OperationRecord) *asynccfg.OperationRecord {
	if rec == nil {
		return nil
	}
	cp := *rec
	return &cp
}
