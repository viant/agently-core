package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	runmodel "github.com/viant/agently-core/model/run"
	"github.com/viant/agently-core/runtime/clienttool"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

// ResumeContinuation resumes the same admitted logical turn at its next model
// iteration after external results have been persisted. The caller must hold
// the durable protocol claim for the continuation. This method independently
// claims the native run through its existing conditional writer, preserving
// heartbeat, binding, execution and finalization without Query/startTurn.
func (s *Service) ResumeContinuation(ctx context.Context, input *QueryInput, turnID string, nextIteration int, output *QueryOutput) error {
	if s == nil || s.conversation == nil || s.dataService == nil || input == nil || output == nil {
		return fmt.Errorf("continuation requires agent, conversation and run stores, input and output")
	}
	conversationID := strings.TrimSpace(input.ConversationID)
	turnID = strings.TrimSpace(turnID)
	if conversationID == "" || turnID == "" {
		return fmt.Errorf("continuation conversation and turn are required")
	}
	if session := clienttool.FromContext(ctx); session != nil && (session.Error() != nil || len(session.Pending()) > 0) {
		return fmt.Errorf("continuation requires a fresh client tool session")
	}
	ctx = s.bindAuthFromInputContext(ctx, input)
	ctx = bindEffectiveUserFromInput(ctx, input)
	conv, err := s.conversation.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeModelCall(true), apiconv.WithIncludeToolCall(true))
	if err != nil {
		return err
	}
	if conv == nil || conv.Id != conversationID || (conv.ScheduleId != nil && strings.TrimSpace(*conv.ScheduleId) != "") {
		return fmt.Errorf("continuation conversation is not an interactive root")
	}
	if conv.HasConversationParent() {
		dependency, hasDependency := clienttool.ContinuationDependencyFromContext(ctx)
		detached, hasDetached := clienttool.DetachedContinuationFromContext(ctx)
		if hasDependency && dependency.ChildConversationID == conversationID && dependency.ChildTurnID == turnID {
			if conv.ConversationParentId == nil || *conv.ConversationParentId != dependency.ParentCall.ConversationID || conv.ConversationParentTurnId == nil || *conv.ConversationParentTurnId != dependency.ParentCall.TurnID {
				return fmt.Errorf("linked continuation requires matching durable dependency")
			}
			parentMessage, err := s.conversation.GetMessage(ctx, dependency.ParentCall.ToolMessageID, apiconv.WithIncludeToolCall(true))
			if err != nil {
				return err
			}
			if parentMessage == nil || parentMessage.Role != "tool" || parentMessage.ConversationId != dependency.ParentCall.ConversationID || parentMessage.TurnId == nil || *parentMessage.TurnId != dependency.ParentCall.TurnID {
				return fmt.Errorf("linked continuation parent call identity mismatch")
			}
			parentCall := messageToolCall(parentMessage)
			if parentCall == nil || parentCall.OpId != dependency.ParentCall.ID || parentCall.Status != "waiting_for_user" {
				return fmt.Errorf("linked continuation parent call is not waiting")
			}
		} else if hasDetached && detached.ChildConversationID == conversationID && detached.ChildTurnID == turnID {
			if detached.ParentProtocolRunID == "" || detached.ExecutionMode != "detach" || detached.ParentConversationID == "" || detached.ParentTurnID == "" || conv.ConversationParentId == nil || *conv.ConversationParentId != detached.ParentConversationID || conv.ConversationParentTurnId == nil || *conv.ConversationParentTurnId != detached.ParentTurnID {
				return fmt.Errorf("detached continuation requires matching trusted origin")
			}
		} else {
			return fmt.Errorf("linked continuation requires matching durable dependency or detached origin")
		}
	}
	var persistedTurn *apiconv.Turn
	for _, candidate := range conv.GetTranscript() {
		if candidate != nil && candidate.Id == turnID {
			persistedTurn = candidate
			break
		}
	}
	if persistedTurn == nil || persistedTurn.ConversationId != conversationID || persistedTurn.Status != "waiting_for_user" {
		return fmt.Errorf("continuation turn is not waiting for user")
	}
	run, err := s.dataService.GetRun(ctx, turnID, nil)
	if err != nil {
		return err
	}
	if run == nil || run.ConversationId == nil || *run.ConversationId != conversationID || run.Status != "completed" {
		return fmt.Errorf("continuation native run is not completed at a wait boundary")
	}
	principal := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if principal == "" {
		principal = strings.TrimSpace(input.UserId)
	}
	if principal == "" {
		return fmt.Errorf("continuation principal is required")
	}
	if run.EffectiveUserId != nil && strings.TrimSpace(*run.EffectiveUserId) != "" && principal != strings.TrimSpace(*run.EffectiveUserId) {
		return fmt.Errorf("continuation principal does not own the run")
	}
	if conv.CreatedByUserId != nil && strings.TrimSpace(*conv.CreatedByUserId) != "" && principal != strings.TrimSpace(*conv.CreatedByUserId) {
		return fmt.Errorf("continuation principal does not own the conversation")
	}
	latestIteration := run.Iteration
	for _, raw := range persistedTurn.Message {
		if raw != nil && raw.ModelCall != nil && raw.ModelCall.Status == "completed" {
			if value := modelCallIteration((*apiconv.Message)(raw)); value > latestIteration {
				latestIteration = value
			}
		}
	}
	if latestIteration < 1 {
		return fmt.Errorf("continuation run has no persisted model iteration")
	}
	expectedIteration := latestIteration + 1
	if nextIteration == 0 {
		nextIteration = expectedIteration
	}
	if nextIteration != expectedIteration {
		return fmt.Errorf("continuation iteration must be %d", expectedIteration)
	}
	for _, call := range indexTurnToolCalls(persistedTurn) {
		if call.status == "waiting_for_user" {
			return fmt.Errorf("continuation client tool %s still awaits a result", call.opID)
		}
	}

	input.AgentID = resolveResumeAgentID(valueOrEmpty(run.AgentId), persistedTurn.AgentIdUsed, conv)
	if input.AgentID == "" {
		return fmt.Errorf("continuation agent identity is missing")
	}
	input.Agent = nil
	input.MessageID = turnID
	input.SkipInitialUserMessage = true
	input.Query = resumeTurnQuery(persistedTurn)
	input.DisplayQuery = input.Query
	if err = s.ensureAgent(ctx, input); err != nil {
		return err
	}
	if input.ModelOverride == "" && persistedTurn.ModelOverride != nil {
		input.ModelOverride = *persistedTurn.ModelOverride
	}
	turn := runtimerequestctx.TurnMeta{Assistant: input.Agent.ID, ConversationID: conversationID, TurnID: turnID}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	ctx = runtimerequestctx.WithTurnMeta(ctx, turn)
	waiting, err := s.turnAwaitingUserAction(ctx, turn)
	if err != nil {
		return err
	}
	if waiting {
		return fmt.Errorf("continuation still has open user actions")
	}
	owner, err := s.claimContinuationRun(ctx, run)
	if err != nil {
		return err
	}
	ctx, leaseCancel := context.WithCancel(ctx)
	defer leaseCancel()
	ctx = withRunLease(ctx, s.newRunLease(owner, leaseCancel))
	ctx = s.resumeExecutionContext(ctx, input)
	ctx, cancel := s.registerTurnCancel(ctx, turn)
	defer cancel()
	stopHeartbeat := s.startRunHeartbeat(ctx, turn)
	finalize := func(status string, cause error) error {
		return errors.Join(cause, stopRunHeartbeatThen(stopHeartbeat, func() error { return s.finalizeTurn(ctx, turn, status, cause) }))
	}
	reopen := apiconv.NewTurn()
	reopen.SetId(turnID)
	reopen.SetStatus("running")
	if err = s.conversation.PatchTurn(ctx, reopen); err != nil {
		return finalize("failed", err)
	}
	if err = s.patchConversationStatus(ctx, conversationID, "running"); err != nil {
		return finalize("failed", err)
	}
	output.ConversationID = conversationID
	output.TurnID = turnID
	output.MessageID = turnID
	status, runErr := s.runPlanAndStatusFrom(ctx, input, output, planLoopStart{Iteration: nextIteration, Phase: planLoopPhaseModel})
	output.ExecutionStatus = status
	if lease := runLeaseFromContext(ctx); lease != nil && !lease.Active() {
		stopHeartbeat()
		return errRunLeaseLost
	}
	return finalize(status, runErr)
}

// claimContinuationRun uses the existing Datly ownership claim shape first,
// then an owner-guarded phase transition. Claims never change result fields.
func (s *Service) claimContinuationRun(ctx context.Context, run *runmodel.RunRowsView) (string, error) {
	if run == nil || run.LeaseOwner == nil || strings.TrimSpace(*run.LeaseOwner) == "" {
		return "", fmt.Errorf("continuation run lacks an ownership token")
	}
	owner := s.newRunLeaseOwner()
	observedAttempt := run.Attempt
	claim := &runmodel.MutableRunView{}
	claim.SetId(run.Id)
	claim.SetAttempt(observedAttempt + 1)
	claim.SetLeaseOwner(owner)
	claim.SetLeaseUntil(runLeaseTimestamp(time.Now()).Add(s.runLeaseDuration()))
	claim.SetLastHeartbeatAt(runLeaseTimestamp(time.Now()))
	claim.SetCondition(runmodel.RunPatchCondition{Status: "completed", LeaseOwner: run.LeaseOwner, Attempt: &observedAttempt})
	if _, err := s.dataService.PatchRuns(ctx, []*runmodel.MutableRunView{claim}); err != nil {
		return "", err
	}
	confirmed, err := s.dataService.GetRun(ctx, run.Id, nil)
	if err != nil {
		return "", err
	}
	if confirmed == nil || confirmed.LeaseOwner == nil || *confirmed.LeaseOwner != owner || confirmed.Status != "completed" {
		return "", fmt.Errorf("continuation native run claim lost")
	}
	phase := &runmodel.MutableRunView{}
	phase.SetId(run.Id)
	phase.SetStatus("running")
	phase.CompletedAt = nil
	phase.Has.CompletedAt = true
	phase.SetCondition(runmodel.RunPatchCondition{LeaseOwner: &owner})
	if _, err = s.dataService.PatchRuns(ctx, []*runmodel.MutableRunView{phase}); err != nil {
		return "", err
	}
	confirmed, err = s.dataService.GetRun(ctx, run.Id, nil)
	if err != nil {
		return "", err
	}
	if confirmed == nil || confirmed.LeaseOwner == nil || *confirmed.LeaseOwner != owner || confirmed.Status != "running" {
		return "", fmt.Errorf("continuation native run ownership lost while reopening")
	}
	return owner, nil
}
