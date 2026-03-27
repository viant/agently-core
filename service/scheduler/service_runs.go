package scheduler

import (
	"context"
	"log"
	"strings"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	schrun "github.com/viant/agently-core/pkg/agently/scheduler/run"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	"github.com/viant/agently-core/runtime/memory"
	agentsvc "github.com/viant/agently-core/service/agent"
)

func (s *Service) executeRun(ctx context.Context, row *schedulepkg.ScheduleView, runID string, scheduledFor time.Time) {
	if s == nil || row == nil || s.agent == nil {
		return
	}
	s.ensureLeaseConfig()

	runCtx := memory.MergeDiscoveryMode(ctx, memory.DiscoveryMode{
		Scheduler:     true,
		ScheduleID:    strings.TrimSpace(row.Id),
		ScheduleRunID: strings.TrimSpace(runID),
	})
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "user_cred detected ref_kind=%q", userCredRefKind(cred))
		var err error
		runCtx, err = s.applyUserCred(runCtx, cred)
		if err != nil {
			logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "user_cred apply failed err=%v", err)
		}
	}

	userID := scheduleUserID(runCtx, row)
	if userID != "" && strings.TrimSpace(iauth.EffectiveUserID(runCtx)) == "" {
		runCtx = iauth.WithUserInfo(runCtx, &iauth.UserInfo{Subject: userID})
	}
	logAuthRunf(row.Id, runID, scheduleUserID(runCtx, row), "using effective user")

	stopHeartbeat := func() {}
	defer s.releaseRunLease(context.Background(), runID)
	defer func() { stopHeartbeat() }()
	if claimed, claimErr := s.tryClaimRunLease(runCtx, runID, time.Now().UTC()); claimErr != nil {
		log.Printf("scheduler: claim run lease schedule=%s run=%s owner=%s: %v", row.Id, runID, strings.TrimSpace(s.leaseOwner), claimErr)
	} else if claimed {
		stopHeartbeat = s.startRunLeaseHeartbeat(runCtx, runID)
	}

	input := &agentsvc.QueryInput{
		MessageID:     runID,
		AgentID:       strings.TrimSpace(row.AgentRef),
		UserId:        userID,
		Query:         schedulePrompt(row),
		ModelOverride: strings.TrimSpace(valueOrEmpty(row.ModelOverride)),
		ScheduleId:    strings.TrimSpace(row.Id),
	}
	output := &agentsvc.QueryOutput{}
	queryCtx, queryCancel := context.WithTimeout(runCtx, scheduleExecutionTimeout(row))
	err := s.agent.Query(queryCtx, input, output)
	queryCancel()

	runPatch := &agrunwrite.MutableRunView{}
	runPatch.SetId(runID)
	runPatch.SetScheduleID(row.Id)
	runPatch.SetConversationKind("scheduled")
	runPatch.SetScheduledFor(scheduledFor.UTC())
	runPatch.SetAgentID(strings.TrimSpace(row.AgentRef))
	if userID != "" {
		runPatch.SetEffectiveUserID(userID)
	}
	if cred := strings.TrimSpace(valueOrEmpty(row.UserCredURL)); cred != "" {
		runPatch.SetUserCredURL(cred)
	}
	if output.ConversationID != "" {
		runPatch.SetConversationID(output.ConversationID)
	}
	if patchErr := s.store.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{runPatch}); patchErr != nil {
		log.Printf("scheduler: patch run metadata schedule=%s run=%s: %v", row.Id, runID, patchErr)
	}

	status := "succeeded"
	errMsg := ""
	if err != nil {
		status = "failed"
		errMsg = err.Error()
		failPatch := &agrunwrite.MutableRunView{}
		failPatch.SetId(runID)
		failPatch.SetStatus(status)
		failPatch.SetErrorMessage(errMsg)
		failPatch.SetCompletedAt(time.Now().UTC())
		if patchErr := s.store.PatchRuns(context.Background(), []*agrunwrite.MutableRunView{failPatch}); patchErr != nil {
			log.Printf("scheduler: patch failed run schedule=%s run=%s: %v", row.Id, runID, patchErr)
		}
		log.Printf("scheduler: execute schedule=%s run=%s: %v", row.Id, runID, err)
	}

	if output.ConversationID != "" {
		s.annotateConversation(context.Background(), row, output.ConversationID, runID)
	}
	if patchErr := s.patchScheduleResult(context.Background(), row, status, errMsg, timePtrUTC(time.Now().UTC()), false, time.Now().UTC()); patchErr != nil {
		log.Printf("scheduler: patch schedule result schedule=%s run=%s: %v", row.Id, runID, patchErr)
	}
}

func (s *Service) getRunsForDueCheck(ctx context.Context, scheduleID string, scheduledFor time.Time, includeScheduledSlot bool) ([]*schrun.RunView, error) {
	var runs []*schrun.RunView
	seen := map[string]struct{}{}
	add := func(items []*schrun.RunView) {
		for _, item := range items {
			if item == nil {
				continue
			}
			id := strings.TrimSpace(item.Id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			runs = append(runs, item)
		}
	}
	if includeScheduledSlot {
		slotRuns, err := s.store.ListRunsForDue(ctx, scheduleID, &scheduledFor, nil)
		if err != nil {
			return nil, err
		}
		add(slotRuns)
	}
	activeRuns, err := s.store.ListRunsForDue(ctx, scheduleID, nil, []string{"succeeded", "failed", "skipped", "canceled"})
	if err != nil {
		return nil, err
	}
	add(activeRuns)
	return runs, nil
}
