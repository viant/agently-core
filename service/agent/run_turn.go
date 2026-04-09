package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
)

func (s *Service) startTurn(ctx context.Context, turn runtimerequestctx.TurnMeta, scheduleID string) error {
	rec := apiconv.NewTurn()
	rec.SetId(turn.TurnID)
	rec.SetConversationID(turn.ConversationID)
	rec.SetStatus("running")
	if starterID := strings.TrimSpace(turn.TurnID); starterID != "" {
		rec.SetStartedByMessageID(starterID)
	}
	if agentID := strings.TrimSpace(turn.Assistant); agentID != "" {
		rec.SetAgentIDUsed(agentID)
	}
	rec.SetRunID(turn.TurnID)
	rec.SetCreatedAt(time.Now())
	debugf("agent.startTurn convo=%q turn_id=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
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
	debugf("agent.addUserMessage convo=%q turn_id=%q user_id=%q content_len=%d content_head=%q content_tail=%q raw_len=%d raw_head=%q raw_tail=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(userID), len(content), headString(content, 512), tailString(content, 512), len(raw), headString(raw, 512), tailString(raw, 512))
	_, err := s.addMessage(ctx, turn, "user", userID, content, rawPtr, "task", turn.TurnID)
	if err != nil {
		return fmt.Errorf("failed to add message: %w", err)
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
	if handled, err := s.maybeForceInitialRepoAnalysisDelegation(ctx, input, output); handled {
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "canceled", err
			}
			return "failed", err
		}
		return "succeeded", nil
	}
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
		errorf("agent.finalizeTurn patch run failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), runPatchErr)
	}
	conversationPatchErr := s.conversation.PatchConversations(patchCtx, convw.NewConversationStatus(turn.ConversationID, status))
	if conversationPatchErr != nil {
		errorf("agent.finalizeTurn patch conversation failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), conversationPatchErr)
	}
	// Patch the turn last. PatchTurn emits the terminal SSE event, so this ordering
	// ensures transcript-level state (conversation + run) is already durable when
	// the client observes turn_completed/turn_failed/turn_canceled.
	turnPatchErr := s.conversation.PatchTurn(patchCtx, upd)
	if turnPatchErr == nil {
		s.patchStarterMessageTerminalStatus(patchCtx, turn, status)
	}
	if turnPatchErr != nil {
		errorf("agent.finalizeTurn patch turn failed convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), turnPatchErr)
	}

	errs := make([]error, 0, 3)
	if runErr != nil {
		errorf("agent.finalizeTurn convo=%q turn_id=%q status=%q err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status), runErr)
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

	infof("agent.finalizeTurn convo=%q turn_id=%q status=%q", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), strings.TrimSpace(status))
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
		_ = s.conversation.PatchConversations(ctx, (*apiconv.MutableConversation)(&mw))
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
	if _, err := s.dataService.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
		warnf("agent.updateRunIteration failed convo=%q turn_id=%q iter=%d err=%v", strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID), iteration, err)
	}
}

func (s *Service) replaceInterimContentForElicitation(ctx context.Context, turn *runtimerequestctx.TurnMeta, genOutput *core.GenerateOutput, elicitMessage string) {
	if s.conversation == nil || turn == nil {
		return
	}
	cleanContent := elicitMessage
	if cleanContent == "" {
		cleanContent = "Additional input required."
	}
	msgID := strings.TrimSpace(genOutput.MessageID)
	if msgID == "" {
		msgID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	if msgID == "" {
		msgID = s.findLastInterimAssistantMessageID(ctx, turn.ConversationID, turn.TurnID)
	}
	if msgID == "" {
		return
	}
	msg := apiconv.NewMessage()
	msg.SetId(msgID)
	msg.SetConversationID(turn.ConversationID)
	msg.SetContent(cleanContent)
	msg.SetRawContent(cleanContent)
	_ = s.conversation.PatchMessage(ctx, msg)
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
	if s == nil || s.conversation == nil {
		return false, nil
	}
	conversationID := strings.TrimSpace(turn.ConversationID)
	turnID := strings.TrimSpace(turn.TurnID)
	if conversationID == "" || turnID == "" {
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

func statusIndicatesAwaitingUser(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "pending", "waiting_for_user":
		return true
	default:
		return false
	}
}
