package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/agently-core/internal/logx"
	"github.com/viant/agently-core/internal/textutil"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

func (s *Service) startTurn(ctx context.Context, turn runtimerequestctx.TurnMeta, scheduleID string) error {
	rec := apiconv.NewTurn()
	rec.SetId(turn.TurnID)
	rec.SetConversationID(turn.ConversationID)
	rec.SetStatus("running")
	if starterID := strings.TrimSpace(turn.ParentMessageID); starterID != "" {
		rec.SetStartedByMessageID(starterID)
	}
	if agentID := strings.TrimSpace(turn.Assistant); agentID != "" {
		rec.SetAgentIDUsed(agentID)
	}
	rec.SetRunID(turn.TurnID)
	rec.SetCreatedAt(time.Now())
	logx.Infof("conversation", "agent.startTurn convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	turnErr := s.conversation.PatchTurn(ctx, rec)
	runErr := s.ensureRunRecord(ctx, turn, "running", strings.TrimSpace(scheduleID))
	convErr := s.conversation.PatchConversations(ctx, convw.NewConversationStatus(turn.ConversationID, "running"))
	if turnErr == nil && convErr == nil && runErr == nil {
		return nil
	}
	if turnErr != nil && convErr != nil && runErr != nil {
		return errors.Join(
			fmt.Errorf("failed to create turn: %w", turnErr),
			fmt.Errorf("failed to create run: %w", runErr),
			fmt.Errorf("failed to update conversation status: %w", convErr),
		)
	}
	if turnErr != nil {
		return fmt.Errorf("failed to create turn: %w", turnErr)
	}
	if runErr != nil {
		return fmt.Errorf("failed to create run: %w", runErr)
	}
	return fmt.Errorf("failed to update conversation status: %w", convErr)
}

func (s *Service) addUserMessage(ctx context.Context, turn *runtimerequestctx.TurnMeta, userID, content, raw string) error {
	var rawPtr *string
	if strings.TrimSpace(raw) != "" {
		rawCopy := raw
		rawPtr = &rawCopy
	}
	logx.Infof("conversation", "agent.addUserMessage convo=%q turn_id=%q user_id=%q content_len=%d content_head=%q content_tail=%q raw_len=%d raw_head=%q raw_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(userID), len(content), textutil.Head(content, 512), textutil.Tail(content, 512), len(raw), textutil.Head(raw, 512), textutil.Tail(raw, 512))
	messageID, err := s.addMessage(ctx, turn, "user", userID, content, rawPtr, "task", "")
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
	}
	turn.ParentMessageID = strings.TrimSpace(messageID)
	return nil
}

func (s *Service) patchTurnStartedByMessageID(ctx context.Context, turn runtimerequestctx.TurnMeta) error {
	starterID := strings.TrimSpace(turn.ParentMessageID)
	if starterID == "" {
		return nil
	}
	upd := apiconv.NewTurn()
	upd.SetId(turn.TurnID)
	upd.SetConversationID(turn.ConversationID)
	upd.SetStartedByMessageID(starterID)
	if err := s.conversation.PatchTurn(ctx, upd); err != nil {
		return fmt.Errorf("failed to update turn starter message: %w", err)
	}
	return nil
}

func (s *Service) persistInitialUserMessage(ctx context.Context, turn *runtimerequestctx.TurnMeta, userID, content, raw string) error {
	if err := s.addUserMessage(ctx, turn, userID, content, raw); err != nil {
		return err
	}
	if err := s.patchTurnStartedByMessageID(ctx, *turn); err != nil {
		return err
	}
	return nil
}

func (s *Service) processAttachments(ctx context.Context, turn runtimerequestctx.TurnMeta, input *QueryInput) error {
	if len(input.Attachments) == 0 {
		return nil
	}
	modelName := ""
	if input.ModelOverride != "" {
		modelName = input.ModelOverride
	} else if input.Agent != nil {
		modelName = input.Agent.Model
	}
	model, _ := s.llm.ModelFinder().Find(ctx, modelName)
	var limit int64
	if input.Agent != nil && input.Agent.Attachment != nil && input.Agent.Attachment.LimitBytes > 0 {
		limit = input.Agent.Attachment.LimitBytes
	} else {
		limit = s.llm.ProviderAttachmentLimit(model)
	}
	used := s.llm.AttachmentUsage(turn.ConversationID)
	var appended int64
	for _, att := range input.Attachments {
		if att == nil || len(att.Data) == 0 {
			continue
		}
		if limit > 0 {
			remain := limit - used - appended
			size := int64(len(att.Data))
			if remain <= 0 || size > remain {
				name := strings.TrimSpace(att.Name)
				if name == "" {
					name = "(unnamed)"
				}
				limMB := float64(limit) / (1024.0 * 1024.0)
				usedMB := float64(used+appended) / (1024.0 * 1024.0)
				curMB := float64(size) / (1024.0 * 1024.0)
				return fmt.Errorf("attachments exceed agent cap: limit %.3f MB, used %.3f MB, current (%s) %.3f MB", limMB, usedMB, name, curMB)
			}
		}
		if err := s.addAttachment(ctx, turn, att); err != nil {
			return err
		}
		appended += int64(len(att.Data))
	}
	if appended > 0 {
		s.llm.SetAttachmentUsage(turn.ConversationID, used+appended)
		_ = s.updateAttachmentUsageMetadata(ctx, turn.ConversationID, used+appended)
	}
	return nil
}

func (s *Service) runPlanAndStatus(ctx context.Context, input *QueryInput, output *QueryOutput) (string, error) {
	if err := s.runPlanLoop(ctx, input, output); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "canceled", err
		}
		return "failed", err
	}
	if turn, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		waitingForUser, err := s.turnAwaitingUserAction(ctx, turn)
		if err != nil {
			return "failed", err
		}
		if waitingForUser {
			return "waiting_for_user", nil
		}
	}
	if output != nil && output.Plan != nil && output.Plan.IsEmpty() && strings.TrimSpace(output.Content) == "" {
		return "canceled", context.Canceled
	}
	return "succeeded", nil
}

func (s *Service) finalizeTurn(ctx context.Context, turn runtimerequestctx.TurnMeta, status string, runErr error) error {
	var emsg string
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		emsg = runErr.Error()
	}
	patchCtx, cancelPatch := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancelPatch()
	upd := apiconv.NewTurn()
	upd.SetId(turn.TurnID)
	upd.SetStatus(status)
	if emsg != "" {
		upd.SetErrorMessage(emsg)
	}

	runPatchErr := s.patchRunTerminalState(patchCtx, turn, status, emsg)
	if runPatchErr != nil {
		logx.Errorf("conversation", "agent.finalizeTurn patch run failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), runPatchErr)
	}
	conversationPatchErr := s.conversation.PatchConversations(patchCtx, convw.NewConversationStatus(turn.ConversationID, status))
	if conversationPatchErr == nil && s.dataService != nil {
		_, dsErr := s.dataService.PatchConversations(patchCtx, []*convw.Conversation{
			convw.NewConversationStatus(turn.ConversationID, status),
		})
		if dsErr != nil {
			logx.Warnf("conversation", "agent.finalizeTurn patch conversation data-service failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), dsErr)
		}
	}
	if conversationPatchErr != nil {
		logx.Errorf("conversation", "agent.finalizeTurn patch conversation failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), conversationPatchErr)
	}
	// Patch the turn last. PatchTurn emits the terminal SSE event, so this ordering
	// ensures transcript-level state (conversation + run) is already durable when
	// the client observes turn_completed/turn_failed/turn_canceled.
	turnPatchErr := s.conversation.PatchTurn(patchCtx, upd)
	if turnPatchErr == nil {
		s.patchStarterMessageTerminalStatus(patchCtx, turn, status)
	}
	if turnPatchErr != nil {
		logx.Errorf("conversation", "agent.finalizeTurn patch turn failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), turnPatchErr)
	}

	errs := make([]error, 0, 3)
	if runErr != nil {
		logx.Errorf("conversation", "agent.finalizeTurn convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), runErr)
		errs = append(errs, runErr)
	}
	if turnPatchErr != nil {
		errs = append(errs, fmt.Errorf("failed to update turn: %w", turnPatchErr))
	}
	if runPatchErr != nil {
		errs = append(errs, fmt.Errorf("failed to update run: %w", runPatchErr))
	}
	if conversationPatchErr != nil {
		errs = append(errs, fmt.Errorf("failed to update conversation: %w", conversationPatchErr))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	logx.Infof("conversation", "agent.finalizeTurn convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
	runtimerequestctx.CleanupTurn(turn.TurnID)
	s.triggerQueueDrain(turn.ConversationID)
	return nil
}

func (s *Service) updateDefaultModel(ctx context.Context, turn runtimerequestctx.TurnMeta, output *QueryOutput) error {
	if strings.TrimSpace(output.Model) == "" {
		return nil
	}
	w := &convw.Conversation{Has: &convw.ConversationHas{}}
	w.SetId(turn.ConversationID)
	w.SetDefaultModel(output.Model)
	if s.conversation != nil {
		mw := convw.Conversation(*w)
		if err := s.conversation.PatchConversations(ctx, (*apiconv.MutableConversation)(&mw)); err != nil {
			// Updating the default model is a best-effort write; the turn has
			// already succeeded, so log and continue rather than unwind.
			logx.Warnf("conversation", "agent.updateDefaultModel failed convo=%q model=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(output.Model), err)
		}
	}
	return nil
}

func (s *Service) executeChainsAfter(ctx context.Context, input *QueryInput, output *QueryOutput, turn runtimerequestctx.TurnMeta, conv *apiconv.Conversation, status string) error {
	cc := NewChainContext(input, output, &turn)
	cc.Conversation = conv
	return s.executeChains(ctx, cc, status)
}

func (s *Service) captureSecurityContext(ctx context.Context, input *QueryInput) {
	if s.dataService == nil || input == nil {
		return
	}
	runID := strings.TrimSpace(input.MessageID)
	if runID == "" {
		return
	}
	secData, err := token.MarshalSecurityContext(ctx)
	if err != nil || secData == "" {
		return
	}
	run := &agrunwrite.MutableRunView{}
	run.SetId(runID)
	run.SetSecurityContext(secData)
	userID := authctx.EffectiveUserID(ctx)
	if userID != "" {
		run.SetEffectiveUserID(userID)
	}
	_, _ = s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run})
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (s *Service) ensureRunRecord(ctx context.Context, turn runtimerequestctx.TurnMeta, status, scheduleID string) error {
	if s == nil || s.dataService == nil {
		return nil
	}
	now := time.Now()
	run := &agrunwrite.MutableRunView{}
	run.SetId(turn.TurnID)
	run.SetTurnID(turn.TurnID)
	run.SetConversationID(turn.ConversationID)
	if scheduleID != "" {
		run.SetScheduleID(scheduleID)
		run.SetConversationKind("scheduled")
	} else {
		run.SetConversationKind("interactive")
		run.SetCreatedAt(now)
		s.populateInteractiveRunRuntime(run, now)
	}
	run.SetStatus(status)
	run.SetIteration(1)
	run.SetStartedAt(now)
	_, err := s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run})
	return err
}

func (s *Service) updateRunIteration(ctx context.Context, turn runtimerequestctx.TurnMeta, iteration int) {
	if s == nil || s.dataService == nil || iteration <= 0 {
		return
	}
	run := &agrunwrite.MutableRunView{}
	run.SetId(turn.TurnID)
	run.SetIteration(iteration)
	run.SetStatus("running")
	s.touchInteractiveRunHeartbeat(run, time.Now())
	if _, err := s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
		logx.Warnf("conversation", "agent.updateRunIteration failed convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iteration, err)
	}
}

func (s *Service) startRunHeartbeat(ctx context.Context, turn runtimerequestctx.TurnMeta) func() {
	if s == nil || s.dataService == nil || strings.TrimSpace(turn.TurnID) == "" {
		return func() {}
	}
	interval := s.runHeartbeatEvery
	if interval <= 0 {
		if s.runHeartbeatIntervalSec > 0 {
			interval = time.Duration(s.runHeartbeatIntervalSec) * time.Second / 2
		}
		if interval <= 0 {
			interval = 30 * time.Second
		}
	}
	if interval < time.Second {
		interval = time.Second
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
				run := &agrunwrite.MutableRunView{}
				run.SetId(turn.TurnID)
				s.touchInteractiveRunHeartbeat(run, time.Now())
				if _, err := s.dataService.PatchRuns(heartbeatCtx, []*agrunwrite.MutableRunView{run}); err != nil {
					logx.Warnf("conversation", "agent.runHeartbeat failed convo=%q turn_id=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), err)
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func (s *Service) populateInteractiveRunRuntime(run *agrunwrite.MutableRunView, now time.Time) {
	if s == nil || run == nil {
		return
	}
	if strings.TrimSpace(s.runWorkerHost) != "" {
		run.SetWorkerHost(strings.TrimSpace(s.runWorkerHost))
	}
	if strings.TrimSpace(s.runLeaseOwner) != "" {
		run.SetLeaseOwner(strings.TrimSpace(s.runLeaseOwner))
	}
	if s.runHeartbeatIntervalSec > 0 {
		run.SetHeartbeatIntervalSec(s.runHeartbeatIntervalSec)
		run.SetLeaseUntil(now.Add(2 * time.Duration(s.runHeartbeatIntervalSec) * time.Second))
	}
	run.SetLastHeartbeatAt(now)
}

func (s *Service) touchInteractiveRunHeartbeat(run *agrunwrite.MutableRunView, now time.Time) {
	if s == nil || run == nil {
		return
	}
	if strings.TrimSpace(s.runLeaseOwner) != "" {
		run.SetLeaseOwner(strings.TrimSpace(s.runLeaseOwner))
	}
	if s.runHeartbeatIntervalSec > 0 {
		run.SetHeartbeatIntervalSec(s.runHeartbeatIntervalSec)
		run.SetLeaseUntil(now.Add(2 * time.Duration(s.runHeartbeatIntervalSec) * time.Second))
	}
	run.SetLastHeartbeatAt(now)
}

func (s *Service) markAssistantMessageInterim(ctx context.Context, turn *runtimerequestctx.TurnMeta, genOutput *core.GenerateOutput) {
	if s == nil || s.conversation == nil || turn == nil {
		return
	}
	msgID := ""
	if genOutput != nil {
		msgID = strings.TrimSpace(genOutput.MessageID)
	}
	if msgID == "" {
		msgID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	if msgID == "" {
		msgID = s.findLastAssistantMessageID(ctx, turn.ConversationID, turn.TurnID)
	}
	if msgID == "" {
		return
	}
	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(turn.ConversationID)
	msg.SetInterim(1)
	_ = s.conversation.PatchMessage(ctx, msg)
	s.archiveOlderInterimAssistantMessages(ctx, turn.ConversationID, turn.TurnID, msgID)
}

func (s *Service) archiveOlderInterimAssistantMessages(ctx context.Context, conversationID, turnID, keepMessageID string) {
	if s == nil || s.conversation == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(turnID) == "" || strings.TrimSpace(keepMessageID) == "" {
		return
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return
	}
	for _, turn := range conv.GetTranscript() {
		if turn == nil || strings.TrimSpace(turn.Id) != strings.TrimSpace(turnID) {
			continue
		}
		for _, message := range turn.Message {
			if message == nil {
				continue
			}
			if strings.TrimSpace(message.Id) == strings.TrimSpace(keepMessageID) {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(message.Role), "assistant") || message.Interim != 1 {
				continue
			}
			if message.Archived != nil && *message.Archived == 1 {
				continue
			}
			mode := strings.ToLower(strings.TrimSpace(valueOrEmpty(message.Mode)))
			switch mode {
			case "chain", "router", "summary":
				continue
			}
			upd := apiconv.NewMessage()
			upd.SetId(message.Id)
			upd.SetConversationID(conversationID)
			upd.SetArchived(1)
			upd.SupersededBy = &keepMessageID
			upd.Has.SupersededBy = true
			_ = s.conversation.PatchMessage(ctx, upd)
		}
		return
	}
}

func (s *Service) findLastInterimAssistantMessageID(ctx context.Context, conversationID, turnID string) string {
	if s.conversation == nil || conversationID == "" || turnID == "" {
		return ""
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return ""
	}
	transcript := conv.GetTranscript()
	for i := len(transcript) - 1; i >= 0; i-- {
		t := transcript[i]
		if t == nil || strings.TrimSpace(t.Id) != turnID {
			continue
		}
		var lastMsgID string
		for _, msg := range t.Message {
			if msg == nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
			if msg.Interim != 1 {
				continue
			}
			lastMsgID = msg.Id
		}
		return lastMsgID
	}
	return ""
}

func (s *Service) findLastAssistantMessageID(ctx context.Context, conversationID, turnID string) string {
	if s.conversation == nil || conversationID == "" || turnID == "" {
		return ""
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return ""
	}
	transcript := conv.GetTranscript()
	for i := len(transcript) - 1; i >= 0; i-- {
		t := transcript[i]
		if t == nil || strings.TrimSpace(t.Id) != turnID {
			continue
		}
		for j := len(t.Message) - 1; j >= 0; j-- {
			msg := t.Message[j]
			if msg == nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") {
				continue
			}
			if strings.TrimSpace(msg.Id) != "" {
				return msg.Id
			}
		}
		return ""
	}
	return ""
}

func (s *Service) patchRunTerminalState(ctx context.Context, turn runtimerequestctx.TurnMeta, status, errorMessage string) error {
	if s == nil || s.dataService == nil {
		return nil
	}
	run := &agrunwrite.MutableRunView{}
	run.SetId(turn.TurnID)
	run.SetStatus(status)
	run.SetCompletedAt(time.Now())
	if strings.TrimSpace(errorMessage) != "" {
		run.SetErrorMessage(errorMessage)
	}
	_, err := s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run})
	return err
}

func (s *Service) turnAwaitingUserAction(ctx context.Context, turn runtimerequestctx.TurnMeta) (bool, error) {
	if s == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	turnID := strings.TrimSpace(turn.TurnID)
	if conversationID == "" || turnID == "" {
		return false, nil
	}
	if s.dataService != nil {
		waiting, err := s.turnAwaitingUserActionData(ctx, conversationID, turnID)
		if err != nil {
			return false, err
		}
		return waiting, nil
	}
	if s.conversation == nil {
		return false, nil
	}
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true))
	if err != nil {
		return false, fmt.Errorf("load conversation %s: %w", conversationID, err)
	}
	if conv == nil {
		return false, nil
	}
	for _, transcriptTurn := range conv.GetTranscript() {
		if transcriptTurn == nil || strings.TrimSpace(transcriptTurn.Id) != turnID {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(transcriptTurn.Status))
		if status == "waiting_for_user" || status == "queued" {
			return true, nil
		}
		for _, msg := range transcriptTurn.GetMessages() {
			if messageAwaitingUserAction(msg) {
				return true, nil
			}
		}
		return false, nil
	}
	return false, nil
}

func (s *Service) turnAwaitingUserActionData(ctx context.Context, conversationID, turnID string) (bool, error) {
	page, err := s.dataService.GetMessagesPage(context.Background(), &agmessagelist.MessageRowsInput{
		ConversationId: conversationID,
		TurnId:         turnID,
		Has: &agmessagelist.MessageRowsInputHas{
			ConversationId: true,
			TurnId:         true,
		},
	}, &data.PageInput{Limit: 1000, Direction: data.DirectionLatest})
	if err != nil {
		return false, fmt.Errorf("load turn messages %s/%s: %w", conversationID, turnID, err)
	}
	if page == nil {
		return false, nil
	}
	for _, msg := range page.Rows {
		if messageRowAwaitingUserAction(msg) {
			return true, nil
		}
	}
	return false, nil
}

func messageAwaitingUserAction(msg *apiconv.Message) bool {
	if msg == nil {
		return false
	}
	if statusIndicatesAwaitingUser(valueOrEmpty(msg.Status)) {
		return true
	}
	for _, toolMsg := range msg.ToolMessage {
		if toolMsg == nil {
			continue
		}
		if toolMsg.ToolCall != nil && statusIndicatesAwaitingUser(toolMsg.ToolCall.Status) {
			return true
		}
	}
	return false
}

func messageRowAwaitingUserAction(msg *agmessagelist.MessageRowsView) bool {
	if msg == nil {
		return false
	}
	return statusIndicatesAwaitingUser(valueOrEmpty(msg.Status))
}

func statusIndicatesAwaitingUser(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "waiting_for_user":
		return true
	default:
		return false
	}
}
