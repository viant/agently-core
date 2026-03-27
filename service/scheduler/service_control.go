package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	convcli "github.com/viant/agently-core/app/store/conversation"
	schedulepkg "github.com/viant/agently-core/pkg/agently/scheduler/schedule"
	schedwrite "github.com/viant/agently-core/pkg/agently/scheduler/schedule/write"
)

func (s *Service) tryClaimRunLease(ctx context.Context, runID string, now time.Time) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return false, nil
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLeaseCallTimeout)
	defer cancel()
	return s.store.TryClaimRun(callCtx, runID, owner, now.Add(s.leaseTTL))
}

func (s *Service) releaseRunLease(ctx context.Context, runID string) {
	if s == nil || s.store == nil {
		return
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return
	}
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLeaseCallTimeout)
	defer cancel()
	if _, err := s.store.ReleaseRunLease(callCtx, runID, owner); err != nil {
		log.Printf("scheduler: release run lease run=%s owner=%s: %v", runID, owner, err)
	}
}

func (s *Service) startRunLeaseHeartbeat(ctx context.Context, runID string) func() {
	if s == nil || s.store == nil {
		return func() {}
	}
	runID = strings.TrimSpace(runID)
	owner := strings.TrimSpace(s.leaseOwner)
	if runID == "" || owner == "" {
		return func() {}
	}
	interval := s.leaseTTL / 2
	if interval < minRunHeartbeatEvery {
		interval = minRunHeartbeatEvery
	}
	heartbeatCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				now := time.Now().UTC()
				claimed, err := s.tryClaimRunLease(heartbeatCtx, runID, now)
				if err != nil {
					log.Printf("scheduler: heartbeat run lease run=%s owner=%s: %v", runID, owner, err)
					continue
				}
				if !claimed {
					log.Printf("scheduler: heartbeat lost run lease run=%s owner=%s", runID, owner)
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Service) cancelConversationAndMark(ctx context.Context, conversationID, status string) error {
	if s == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	errs := make([]error, 0, 2)
	if s.agent != nil {
		if err := s.agent.Terminate(context.WithoutCancel(ctx), conversationID); err != nil {
			errs = append(errs, fmt.Errorf("terminate conversation %s: %w", conversationID, err))
		}
	}
	if s.conversation != nil {
		if err := s.cancelConversationTreeAndMark(ctx, conversationID, strings.TrimSpace(status), map[string]struct{}{}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) cancelConversationTreeAndMark(ctx context.Context, conversationID, status string, visited map[string]struct{}) error {
	if s == nil || s.conversation == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	if _, ok := visited[conversationID]; ok {
		return nil
	}
	visited[conversationID] = struct{}{}
	patchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	conv, err := s.conversation.GetConversation(patchCtx, conversationID, convcli.WithIncludeTranscript(true), convcli.WithIncludeToolCall(true), convcli.WithIncludeModelCall(true))
	if err != nil || conv == nil {
		return err
	}
	errs := make([]error, 0, 4)
	convPatch := convcli.NewConversation()
	convPatch.SetId(conversationID)
	convPatch.SetStatus(status)
	if err := s.conversation.PatchConversations(patchCtx, convPatch); err != nil {
		errs = append(errs, fmt.Errorf("patch conversation status: %w", err))
	}
	if err := s.markConversationTermination(patchCtx, conv, status); err != nil {
		errs = append(errs, err)
	}
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || msg.LinkedConversationId == nil {
				continue
			}
			childID := strings.TrimSpace(*msg.LinkedConversationId)
			if childID == "" {
				continue
			}
			if err := s.cancelConversationTreeAndMark(patchCtx, childID, status, visited); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) markConversationTermination(ctx context.Context, conv *convcli.Conversation, status string) error {
	if s == nil || s.conversation == nil || conv == nil {
		return nil
	}
	now := time.Now().UTC()
	errs := make([]error, 0)
	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		if !isTerminalExecutionStatus(turn.Status) {
			upd := convcli.NewTurn()
			upd.SetId(strings.TrimSpace(turn.Id))
			upd.SetStatus(status)
			if err := s.conversation.PatchTurn(ctx, upd); err != nil {
				errs = append(errs, fmt.Errorf("patch turn %s: %w", strings.TrimSpace(turn.Id), err))
			}
		}
		if assistant := lastAssistantMessage(turn); assistant != nil {
			if assistant.Status == nil || !isTerminalExecutionStatus(*assistant.Status) {
				upd := convcli.NewMessage()
				upd.SetId(strings.TrimSpace(assistant.Id))
				upd.SetConversationID(strings.TrimSpace(assistant.ConversationId))
				if assistant.TurnId != nil && strings.TrimSpace(*assistant.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*assistant.TurnId))
				}
				upd.SetStatus(status)
				if err := s.conversation.PatchMessage(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch message %s: %w", strings.TrimSpace(assistant.Id), err))
				}
			}
		}
		for _, msg := range turn.Message {
			if msg == nil {
				continue
			}
			if msg.ModelCall != nil && (msg.ModelCall.CompletedAt == nil || msg.ModelCall.CompletedAt.IsZero()) {
				upd := convcli.NewModelCall()
				upd.SetMessageID(strings.TrimSpace(msg.ModelCall.MessageId))
				if msg.ModelCall.TurnId != nil && strings.TrimSpace(*msg.ModelCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*msg.ModelCall.TurnId))
				}
				upd.SetStatus(modelCallFinalStatus(msg.ModelCall.Status))
				upd.SetCompletedAt(now)
				if err := s.conversation.PatchModelCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch model call %s: %w", strings.TrimSpace(msg.ModelCall.MessageId), err))
				}
			}
			for _, toolMsg := range msg.ToolMessage {
				if toolMsg == nil || toolMsg.ToolCall == nil {
					continue
				}
				if toolMsg.ToolCall.CompletedAt != nil && !toolMsg.ToolCall.CompletedAt.IsZero() {
					continue
				}
				upd := convcli.NewToolCall()
				upd.SetMessageID(strings.TrimSpace(toolMsg.ToolCall.MessageId))
				upd.SetOpID(strings.TrimSpace(toolMsg.ToolCall.OpId))
				if toolMsg.ToolCall.TurnId != nil && strings.TrimSpace(*toolMsg.ToolCall.TurnId) != "" {
					upd.SetTurnID(strings.TrimSpace(*toolMsg.ToolCall.TurnId))
				}
				upd.SetStatus(toolCallFinalStatus(toolMsg.ToolCall.Status))
				upd.CompletedAt = timePtrUTC(now)
				upd.Has.CompletedAt = true
				if err := s.conversation.PatchToolCall(ctx, upd); err != nil {
					errs = append(errs, fmt.Errorf("patch tool call %s: %w", strings.TrimSpace(toolMsg.ToolCall.MessageId), err))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func lastAssistantMessage(turn *convcli.Turn) *convcli.Message {
	if turn == nil || len(turn.Message) == 0 {
		return nil
	}
	for i := len(turn.Message) - 1; i >= 0; i-- {
		msg := turn.Message[i]
		if msg != nil && strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
			return (*convcli.Message)(msg)
		}
	}
	return nil
}

func modelCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func toolCallFinalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error":
		return "failed"
	case "succeeded", "success", "completed", "done":
		return "succeeded"
	default:
		return "canceled"
	}
}

func isTerminalExecutionStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "cancel", "failed", "error", "succeeded", "success", "completed", "done", "rejected":
		return true
	default:
		return false
	}
}

func (s *Service) patchScheduleResult(ctx context.Context, row *schedulepkg.ScheduleView, lastStatus string, lastError string, lastRunAt *time.Time, updateNext bool, now time.Time) error {
	if s == nil || row == nil {
		return nil
	}
	mut := &schedwrite.Schedule{}
	mut.SetId(row.Id)
	if lastRunAt != nil && !lastRunAt.IsZero() {
		mut.SetLastRunAt(lastRunAt.UTC())
	}
	if strings.TrimSpace(lastStatus) != "" {
		mut.SetLastStatus(strings.TrimSpace(lastStatus))
		if strings.EqualFold(strings.TrimSpace(lastStatus), "succeeded") && strings.TrimSpace(lastError) == "" {
			mut.LastError = nil
			mut.Has.LastError = true
		}
	}
	if strings.TrimSpace(lastError) != "" {
		mut.SetLastError(strings.TrimSpace(lastError))
	}
	if updateNext {
		if err := s.setNextRunAt(row, mut, now); err != nil {
			return err
		}
	}
	return s.store.PatchSchedule(ctx, mut)
}

func (s *Service) setNextRunAt(row *schedulepkg.ScheduleView, mut *schedwrite.Schedule, now time.Time) error {
	switch {
	case strings.EqualFold(strings.TrimSpace(row.ScheduleType), "cron") && row.CronExpr != nil && strings.TrimSpace(*row.CronExpr) != "":
		loc, _ := time.LoadLocation(strings.TrimSpace(row.Timezone))
		if loc == nil {
			loc = time.UTC
		}
		spec, err := parseCron(strings.TrimSpace(*row.CronExpr))
		if err != nil {
			return fmt.Errorf("invalid cron expr for schedule %s: %w", row.Id, err)
		}
		mut.SetNextRunAt(cronNext(spec, now.In(loc)).UTC())
	case row.IntervalSeconds != nil:
		mut.SetNextRunAt(now.Add(time.Duration(*row.IntervalSeconds) * time.Second).UTC())
	case strings.EqualFold(strings.TrimSpace(row.ScheduleType), "adhoc"):
		mut.NextRunAt = nil
		mut.Has.NextRunAt = true
	}
	return nil
}

func (s *Service) ensureLeaseConfig() {
	if s == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_LEASE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			s.leaseTTL = d
		}
	}
	if strings.TrimSpace(s.leaseOwner) != "" {
		return
	}
	if v := strings.TrimSpace(os.Getenv("AGENTLY_SCHEDULER_LEASE_OWNER")); v != "" {
		s.leaseOwner = v
		return
	}
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "unknown-host"
	}
	s.leaseOwner = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString())
}
