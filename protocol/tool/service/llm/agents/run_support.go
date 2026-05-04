package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/logx"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	agrunwrite "github.com/viant/agently-core/pkg/agently/run/write"
	asynccfg "github.com/viant/agently-core/protocol/async"
	toolpol "github.com/viant/agently-core/protocol/tool"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	agentsvc "github.com/viant/agently-core/service/agent"
	coreauth "github.com/viant/agently-core/service/auth"
	intakesvc "github.com/viant/agently-core/service/intake"
	"github.com/viant/agently-core/service/shared/convterm"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
	skillsvc "github.com/viant/agently-core/service/skill"
)

type linkedRun struct {
	parent              runtimerequestctx.TurnMeta
	childConversationID string
	statusMessageID     string
	statusToolName      string
}

type childRunResult struct {
	answer         string
	status         string
	conversationID string
	messageID      string
	err            error
}

type childConversationState struct {
	conversationID         string
	parentConversationID   string
	parentTurnID           string
	agentID                string
	status                 string
	rawStatus              string
	terminal               bool
	errorSummary           string
	createdAt              string
	updatedAt              string
	lastAssistantNarration string
	lastAssistantResponse  string
	hasFinalResponse       bool
	lastMessageAt          string
	lastActivityAt         time.Time
}

const (
	DefaultChildStatusTimeout    = 20 * time.Minute
	DefaultWaitingForUserTimeout = 5 * time.Minute
)

var childStatusNow = time.Now

func (s *Service) tryExternalRun(ctx context.Context, ri *RunInput, ro *RunOutput, intended string) (bool, error) {
	runCtx, err := s.prepareLinkedRun(ctx, ri, "external", false)
	if err != nil {
		return true, err
	}
	extCtx := ctx
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		extCtx = runtimerequestctx.WithConversationID(ctx, runCtx.childConversationID)
		ro.ConversationID = runCtx.childConversationID
	}
	logx.Infof("conversation", "agents.run external invoke agent_id=%q child_convo=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID))
	ans, st, taskID, ctxID, streamSupp, warns, err := s.runExternal(extCtx, ri.AgentID, ri.Objective, ri.Context)
	if err != nil {
		logx.Errorf("conversation", "agents.run external error agent_id=%q child_convo=%q err=%v", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), err)
		s.finalizeRunStatus(ctx, runCtx, "failed")
		if intended == "external" {
			return true, err
		}
		return false, nil
	}
	if taskID == "" && st == "" {
		return false, nil
	}
	logx.Infof("conversation", "agents.run external ok agent_id=%q child_convo=%q status=%q task_id=%q context_id=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(st), strings.TrimSpace(taskID), strings.TrimSpace(ctxID))
	ro.Answer = ans
	ro.Status = st
	ro.TaskID = taskID
	if strings.TrimSpace(ctxID) != "" {
		ro.ContextID = ctxID
	} else {
		ro.ContextID = runCtx.childConversationID
	}
	ro.StreamSupported = streamSupp
	ro.Warnings = append(ro.Warnings, warns...)
	s.finalizeRunStatus(ctx, runCtx, strings.TrimSpace(st))
	return true, nil
}

// DefaultChildAgentTimeout is the maximum duration a child agent run is
// allowed before its context is cancelled. This prevents hung tool calls
// inside the child from blocking the parent agent forever.
const DefaultChildAgentTimeout = 23 * time.Minute

func (s *Service) runInternal(ctx context.Context, ri *RunInput, ro *RunOutput, convID string, depth int) error {
	if s.agent == nil {
		logx.Errorf("conversation", "agents.run internal error: agent runtime not configured")
		return svc.NewMethodNotFoundError("agent runtime not configured")
	}
	runCtx, err := s.prepareLinkedRun(ctx, ri, "internal", true)
	if err != nil {
		return err
	}
	childContext := inheritDelegatedContext(ctx, ri.Context)
	agentID := effectiveRunAgentID(ri)
	qi := &agentsvc.QueryInput{AgentID: agentID, Agent: ri.Agent, Query: normalizedDelegatedObjective(ri), Context: childContext}
	if qi.Agent == nil && s.agent != nil && s.agent.Finder() != nil && agentID != "" {
		if ag, err := s.agent.Finder().Find(ctx, agentID); err == nil && ag != nil {
			qi.Agent = ag
		}
	}
	if agentID != "" {
		childContext = setDelegationDepth(childContext, agentID, depth+1)
		ri.Context = childContext
		qi.Context = childContext
	}
	// Skip rule §2.c (intake-impt.md): caller pre-provides the workspace-intake
	// result. When set, the runtime stores it under the well-known
	// intakesvc.ContextKey with Source="caller-provided"; the agent-intake
	// sidecar (service/agent/intake_query.go) honors this and skips its own
	// LLM call. Validation (authorized agent set, visible skills, allowlisted
	// bundles) runs at downstream gates that already enforce those checks.
	if ri.WorkspaceIntake != nil {
		qi.Context, _ = intakesvc.StoreCallerProvided(qi.Context, ri.WorkspaceIntake)
		ri.Context = qi.Context
	}
	qi.ToolsAllowed = delegatedToolAllowList(ri)
	// Pre-assign the turn ID so profile messages and the agent turn share the
	// same ID.  agent.Query reuses qi.MessageID when it is non-empty.
	if qi.MessageID == "" {
		qi.MessageID = uuid.NewString()
	}
	// Inherit the parent conversation's model selection so child agents use
	// the same model the user selected, not the system default.
	childHasModel := qi.Agent != nil && strings.TrimSpace(qi.Agent.ModelSelection.Model) != ""
	if !childHasModel {
		if parentModel := s.parentConversationModel(ctx); parentModel != "" {
			qi.ModelOverride = parentModel
		}
	}
	if ri.ModelPreferences != nil && !childHasModel {
		qi.ModelPreferences = ri.ModelPreferences
	}
	if ri.ReasoningEffort != nil {
		qi.ReasoningEffort = ri.ReasoningEffort
	}
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		qi.ConversationID = runCtx.childConversationID
		ro.ConversationID = runCtx.childConversationID
	}
	if ri.Async != nil && *ri.Async {
		ro.Status = "running"
		ro.ConversationID = runCtx.childConversationID
		childIn := *qi
		runReq := *ri
		go func(parentCtx context.Context, childIn *agentsvc.QueryInput, childOut *agentsvc.QueryOutput, linked linkedRun, asyncReq *RunInput) {
			if strings.TrimSpace(asyncReq.PromptProfileId) != "" {
				if err := s.resolveProfile(parentCtx, asyncReq, childIn, linked.childConversationID); err != nil {
					result := childRunResult{
						status:         "failed",
						conversationID: linked.childConversationID,
						err:            fmt.Errorf("resolveProfile: %w", err),
					}
					s.finalizeRunStatus(parentCtx, linked, result.status)
					s.surfaceAsyncCompletion(parentCtx, linked, strings.TrimSpace(asyncReq.AgentID), result)
					return
				}
			}
			result := s.executeChildRun(parentCtx, childIn, childOut, linked)
			finalStatus := result.status
			if strings.TrimSpace(finalStatus) == "" {
				if result.err != nil {
					finalStatus = "failed"
				} else {
					finalStatus = "succeeded"
				}
			}
			s.finalizeRunStatus(parentCtx, linked, finalStatus)
			s.surfaceAsyncCompletion(parentCtx, linked, strings.TrimSpace(asyncReq.AgentID), result)
		}(context.WithoutCancel(ctx), &childIn, &agentsvc.QueryOutput{}, runCtx, &runReq)
		logx.Infof("conversation", "agents.run async accepted agent_id=%q child_convo=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID))
		return nil
	}
	// Expand prompt profile: inject instructions, merge bundles, set template.
	// Must run after qi.MessageID and qi.ConversationID are both set.
	if strings.TrimSpace(ri.PromptProfileId) != "" {
		if err := s.resolveProfile(ctx, ri, qi, runCtx.childConversationID); err != nil {
			return fmt.Errorf("resolveProfile: %w", err)
		}
	}
	qo := &agentsvc.QueryOutput{}
	result := s.executeChildRun(ctx, qi, qo, runCtx)
	if result.err != nil {
		return result.err
	}
	ro.Answer = result.answer
	ro.Status = result.status
	if strings.TrimSpace(result.conversationID) != "" {
		ro.ConversationID = result.conversationID
	}
	if ro.ConversationID == "" {
		ro.ConversationID = convID
	}
	ro.MessageID = result.messageID
	s.finalizeRunStatus(ctx, runCtx, result.status)
	logx.Infof("conversation", "agents.run done convo=%q agent_id=%q status=%q", strings.TrimSpace(ro.ConversationID), strings.TrimSpace(ri.AgentID), strings.TrimSpace(ro.Status))
	return nil
}

func (s *Service) executeChildRun(ctx context.Context, qi *agentsvc.QueryInput, qo *agentsvc.QueryOutput, runCtx linkedRun) childRunResult {
	// Detach from parent's tool-execution deadline so the child agent
	// runs with its own independent timeout. Apply a hard deadline so a
	// hung child doesn't block the parent forever.
	// Clear the parent's ModelMessageIDKey so the child's tool_op messages
	// don't inherit the parent's assistant message as their parent_message_id.
	childCtx := context.WithValue(
		toolpol.WithPolicy(context.WithoutCancel(ctx), nil),
		runtimerequestctx.ModelMessageIDKey, "",
	)
	// Child runs must not inherit the parent's conversation/turn context.
	// Agent.Query performs pre-turn work before it seeds a new turn, so bind
	// the child conversation id here and reset turn metadata up front.
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		childCtx = runtimerequestctx.WithConversationID(childCtx, strings.TrimSpace(runCtx.childConversationID))
	}
	childCtx = runtimerequestctx.WithTurnMeta(childCtx, runtimerequestctx.TurnMeta{})
	childCtx = inheritDelegatedAuthContext(childCtx, ctx)
	if childConversationID := strings.TrimSpace(runCtx.childConversationID); childConversationID != "" {
		childCtx = runtimerequestctx.WithConversationID(childCtx, childConversationID)
		childCtx = runtimerequestctx.WithTurnMeta(childCtx, runtimerequestctx.TurnMeta{
			ConversationID: childConversationID,
			TurnID:         uuid.NewString(),
		})
	}
	if qi != nil && qi.Context != nil {
		if name, _ := qi.Context["skillActivationName"].(string); strings.TrimSpace(name) != "" {
			if mode, _ := qi.Context["skillActivationMode"].(string); strings.TrimSpace(mode) != "" {
				childCtx = skillsvc.WithActivationModeOverride(childCtx, name, mode)
			}
		}
	}
	childTimeout := s.ChildTimeout
	if childTimeout <= 0 {
		childTimeout = DefaultChildAgentTimeout
	}
	childCtx, childCancel := context.WithTimeout(childCtx, childTimeout)
	defer childCancel()
	logx.Infof("conversation", "agents.run internal invoke agent_id=%q child_convo=%q timeout=%s", strings.TrimSpace(qi.Actor()), strings.TrimSpace(runCtx.childConversationID), childTimeout)
	if err := s.agent.Query(childCtx, qi, qo); err != nil {
		logx.Errorf("conversation", "agents.run internal error agent_id=%q child_convo=%q err=%v", strings.TrimSpace(qi.Actor()), strings.TrimSpace(runCtx.childConversationID), err)
		return s.resolveChildRunError(ctx, runCtx, qo, err)
	}
	logx.Infof("conversation", "agents.run internal ok agent_id=%q child_convo=%q message_id=%q", strings.TrimSpace(qi.Actor()), strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(qo.MessageID))
	return childRunResult{
		answer:         qo.Content,
		status:         "succeeded",
		conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
		messageID:      qo.MessageID,
	}
}

func inheritDelegatedAuthContext(target, parent context.Context) context.Context {
	if target == nil {
		target = context.Background()
	}
	if parent == nil {
		return target
	}
	if subject := strings.TrimSpace(coreauth.EffectiveUserID(parent)); subject != "" {
		target = coreauth.InjectUser(target, subject)
	}
	if user := authctx.User(parent); user != nil {
		target = authctx.WithUserInfo(target, user)
	}
	if tok := authctx.TokensFromContext(parent); tok != nil {
		target = coreauth.InjectTokens(target, tok)
	}
	return target
}

func (s *Service) resolveChildRunError(ctx context.Context, runCtx linkedRun, qo *agentsvc.QueryOutput, runErr error) childRunResult {
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || s.isCanceledConversation(ctx, runCtx.childConversationID) {
		if s.isCanceledConversation(ctx, runCtx.childConversationID) {
			if state, ok := s.childConversationState(ctx, runCtx.childConversationID); ok {
				if text := strings.TrimSpace(state.lastAssistantResponse); text != "" {
					return childRunResult{
						answer:         text,
						status:         "canceled",
						conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
					}
				}
				if text := strings.TrimSpace(state.lastAssistantNarration); text != "" {
					return childRunResult{
						answer:         text,
						status:         "canceled",
						conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
					}
				}
			}
			return childRunResult{
				status:         "canceled",
				conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
			}
		}
		if outcome, ok := s.completedChildRunOutcome(ctx, runCtx.childConversationID); ok {
			return outcome
		}
		if summary, ok := s.failedChildRunSummary(ctx, runCtx.childConversationID, runErr); ok {
			return childRunResult{
				answer:         summary,
				status:         "failed",
				conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
			}
		}
		return childRunResult{err: context.Canceled}
	}
	if summary, ok := s.failedChildRunSummary(ctx, runCtx.childConversationID, runErr); ok {
		return childRunResult{
			answer:         summary,
			status:         "failed",
			conversationID: firstNonEmptyString(qo.ConversationID, runCtx.childConversationID),
		}
	}
	return childRunResult{err: runErr}
}

func (s *Service) cancelMethod(ctx context.Context, in, out interface{}) error {
	ci, ok := in.(*CancelInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	co, ok := out.(*CancelOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	conversationID := strings.TrimSpace(ci.ConversationID)
	if conversationID == "" {
		return fmt.Errorf("conversationId is required")
	}
	if s != nil && s.cancelReg != nil && s.cancelReg.CancelConversation(conversationID) {
		co.Status = "canceled"
		return nil
	}
	if s == nil || s.conv == nil {
		return svc.NewMethodNotFoundError("conversation not found: " + conversationID)
	}
	conv, err := s.conv.GetConversation(ctx, conversationID, apiconv.WithIncludeTranscript(true))
	if err != nil {
		return err
	}
	if conv == nil {
		return svc.NewMethodNotFoundError("conversation not found: " + conversationID)
	}
	co.Status = strings.TrimSpace(ptrString(conv.Status))
	if co.Status == "" {
		transcript := conv.GetTranscript()
		if len(transcript) > 0 {
			last := transcript[len(transcript)-1]
			if last != nil {
				co.Status = strings.TrimSpace(last.Status)
			}
		}
	}
	if co.Status == "" {
		co.Status = "unknown"
	}
	return nil
}

func delegatedToolAllowList(ri *RunInput) []string {
	return []string{}
}

func normalizedDelegatedObjective(ri *RunInput) string {
	if ri == nil {
		return ""
	}
	return strings.TrimSpace(ri.Objective)
}

func stringValue(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func boolValue(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	value, ok := values[key]
	if !ok || value == nil {
		return false
	}
	switch actual := value.(type) {
	case bool:
		return actual
	case string:
		return strings.EqualFold(strings.TrimSpace(actual), "true")
	default:
		return false
	}
}

func (s *Service) isCanceledConversation(ctx context.Context, conversationID string) bool {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" {
		return false
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil || conv == nil || conv.Status == nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(*conv.Status))
	return status == "canceled" || status == "cancelled"
}

func (s *Service) failedChildRunSummary(ctx context.Context, conversationID string, runErr error) (string, bool) {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" {
		return "", false
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	if err != nil || conv == nil {
		return "", false
	}
	transcript := conv.GetTranscript()
	if len(transcript) == 0 {
		return "", false
	}
	lastTurns := transcript.Last()
	if len(lastTurns) == 0 || lastTurns[0] == nil {
		return "", false
	}
	lastTurn := lastTurns[0]
	status := strings.TrimSpace(lastTurn.Status)
	if status == "" {
		status = "failed"
	}
	var parts []string
	parts = append(parts, "Child agent conversation "+strings.TrimSpace(conversationID)+" ended with status "+status+".")
	if msg := strings.TrimSpace(ptrString(lastTurn.ErrorMessage)); msg != "" {
		parts = append(parts, "Error: "+msg)
	} else if runErr != nil {
		parts = append(parts, "Error: "+strings.TrimSpace(runErr.Error()))
	}
	if summary := strings.TrimSpace(lastAssistantContent(lastTurn)); summary != "" {
		parts = append(parts, "Last assistant content: "+summary)
	}
	return strings.Join(parts, "\n"), true
}

func isSuccessfulStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "completed", "success", "done":
		return true
	default:
		return false
	}
}

func (s *Service) completedChildRunOutcome(ctx context.Context, conversationID string) (childRunResult, bool) {
	state, ok := s.childConversationState(ctx, conversationID)
	if !ok || !isSuccessfulStatus(state.status) {
		return childRunResult{}, false
	}
	return childRunResult{
		answer:         state.lastAssistantResponse,
		status:         "succeeded",
		conversationID: state.conversationID,
	}, true
}

func (s *Service) childConversationState(ctx context.Context, conversationID string) (childConversationState, bool) {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" {
		return childConversationState{}, false
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
	if err != nil || conv == nil {
		return childConversationState{}, false
	}
	state := childConversationState{
		conversationID:       strings.TrimSpace(conv.Id),
		parentConversationID: strings.TrimSpace(ptrString(conv.ConversationParentId)),
		parentTurnID:         strings.TrimSpace(ptrString(conv.ConversationParentTurnId)),
		agentID:              strings.TrimSpace(ptrString(conv.AgentId)),
		status:               strings.TrimSpace(ptrString(conv.Status)),
	}
	state.rawStatus = state.status
	if !conv.CreatedAt.IsZero() {
		state.createdAt = conv.CreatedAt.Format(time.RFC3339Nano)
		state.lastActivityAt = conv.CreatedAt
	}
	if conv.UpdatedAt != nil && !conv.UpdatedAt.IsZero() {
		state.updatedAt = conv.UpdatedAt.Format(time.RFC3339Nano)
		state.lastActivityAt = *conv.UpdatedAt
	}
	preamble, response, hasFinal, lastMessageAt, lastTurnStatus, lastActivityAt := lastAssistantState(conv.GetTranscript())
	if state.status == "" {
		state.status = lastTurnStatus
	}
	if !lastActivityAt.IsZero() {
		state.lastActivityAt = lastActivityAt
	}
	state.lastAssistantNarration = preamble
	state.lastAssistantResponse = response
	state.hasFinalResponse = hasFinal
	state.lastMessageAt = lastMessageAt
	state = normalizeChildConversationState(state, conv.GetTranscript())
	if state.terminal && !isTerminalChildStatus(state.rawStatus) {
		s.terminalizeTimedOutChildConversation(context.WithoutCancel(ctx), conv, state)
	}
	return state, true
}

func (s *Service) terminalizeTimedOutChildConversation(ctx context.Context, conv *apiconv.Conversation, state childConversationState) {
	if s == nil || conv == nil || s.conv == nil {
		return
	}
	conversationID := strings.TrimSpace(conv.Id)
	if conversationID == "" {
		return
	}
	now := time.Now().UTC()

	convPatch := apiconv.NewConversation()
	convPatch.SetId(conversationID)
	convPatch.SetStatus("failed")
	if err := s.conv.PatchConversations(ctx, convPatch); err != nil {
		logx.Warnf("conversation", "agents.status timed-out child patch conversation failed conv=%q err=%v", conversationID, err)
	}
	if err := convterm.PatchExecutionTerminal(ctx, s.conv, conv, "failed"); err != nil {
		logx.Warnf("conversation", "agents.status timed-out child patch execution failed conv=%q err=%v", conversationID, err)
	}

	for _, turn := range conv.GetTranscript() {
		if turn == nil {
			continue
		}
		turnID := strings.TrimSpace(turn.Id)
		if turnID == "" {
			continue
		}
		turnPatch := apiconv.NewTurn()
		turnPatch.SetId(turnID)
		turnPatch.SetConversationID(conversationID)
		turnPatch.SetStatus("failed")
		if msg := strings.TrimSpace(state.errorSummary); msg != "" {
			turnPatch.SetErrorMessage(msg)
		}
		if err := s.conv.PatchTurn(ctx, turnPatch); err != nil {
			logx.Warnf("conversation", "agents.status timed-out child patch turn failed conv=%q turn=%q err=%v", conversationID, turnID, err)
		}
		if s.data != nil {
			run := &agrunwrite.MutableRunView{}
			run.SetId(turnID)
			run.SetStatus("failed")
			run.SetCompletedAt(now)
			if msg := strings.TrimSpace(state.errorSummary); msg != "" {
				run.SetErrorMessage(msg)
			}
			if _, err := s.data.PatchRuns(ctx, []*agrunwrite.MutableRunView{run}); err != nil {
				logx.Warnf("conversation", "agents.status timed-out child patch run failed conv=%q turn=%q err=%v", conversationID, turnID, err)
			}
		}
	}
}

func (s *Service) statusItemFromConversation(conv *apiconv.Conversation) StatusItem {
	if conv == nil {
		return StatusItem{}
	}
	state, ok := s.childConversationState(context.Background(), strings.TrimSpace(conv.Id))
	if !ok {
		return StatusItem{}
	}
	item := StatusItem{
		ConversationID:         state.conversationID,
		ParentConversationID:   state.parentConversationID,
		ParentTurnID:           state.parentTurnID,
		AgentID:                state.agentID,
		Status:                 state.status,
		RawStatus:              state.rawStatus,
		Terminal:               state.terminal,
		Error:                  state.errorSummary,
		CreatedAt:              state.createdAt,
		UpdatedAt:              state.updatedAt,
		LastAssistantNarration: state.lastAssistantNarration,
		LastAssistantResponse:  state.lastAssistantResponse,
		HasFinalResponse:       state.hasFinalResponse,
		LastMessageAt:          state.lastMessageAt,
	}
	return normalizeStatusItem(item)
}

func lastAssistantContent(turn *apiconv.Turn) string {
	if turn == nil {
		return ""
	}
	for i := len(turn.Message) - 1; i >= 0; i-- {
		msg := turn.Message[i]
		if msg == nil || strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" {
			continue
		}
		if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
			return text
		}
		if text := strings.TrimSpace(ptrString(msg.Narration)); text != "" {
			return text
		}
	}
	return ""
}

func ptrString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (s *Service) statusMethod(ctx context.Context, in, out interface{}) error {
	si, ok := in.(*StatusInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	so, ok := out.(*StatusOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	items, err := s.collectStatusItems(ctx, si)
	if err != nil {
		return err
	}
	if convID := strings.TrimSpace(si.ConversationID); convID != "" && len(items) > 0 {
		so.ConversationID = convID
		so.Status = strings.TrimSpace(items[0].Status)
		so.RawStatus = strings.TrimSpace(items[0].RawStatus)
		so.Terminal = items[0].Terminal
		so.Error = strings.TrimSpace(items[0].Error)
		so.Message, so.MessageKind = statusItemMessage(items[0])
	}
	return nil
}

func statusItemMessage(item StatusItem) (string, string) {
	item = normalizeStatusItem(item)
	if !item.Terminal {
		if text := strings.TrimSpace(item.LastAssistantNarration); text != "" {
			return text, "preamble"
		}
		return "", ""
	}
	if item.HasFinalResponse {
		if text := strings.TrimSpace(item.LastAssistantResponse); text != "" {
			return text, "response"
		}
	}
	if text := strings.TrimSpace(item.LastAssistantNarration); text != "" {
		return text, "preamble"
	}
	if text := strings.TrimSpace(item.LastAssistantResponse); text != "" {
		return text, "response"
	}
	return "", ""
}

func normalizeStatusItem(item StatusItem) StatusItem {
	if item.HasFinalResponse {
		item.LastAssistantNarration = ""
		return item
	}
	item.LastAssistantResponse = ""
	return item
}

func (s *Service) collectStatusItems(ctx context.Context, in *StatusInput) ([]StatusItem, error) {
	if s == nil || s.conv == nil || in == nil {
		return nil, nil
	}
	if convID := strings.TrimSpace(in.ConversationID); convID != "" {
		conv, err := s.conv.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
		if err != nil {
			if s.runExternal != nil {
				return nil, svc.NewMethodNotFoundError("llm/agents:status unsupported for external agent conversations: " + convID)
			}
			return nil, err
		}
		if conv == nil {
			if s.runExternal != nil {
				return nil, svc.NewMethodNotFoundError("llm/agents:status unsupported for external agent conversations: " + convID)
			}
			return nil, nil
		}
		return []StatusItem{s.statusItemFromConversation(conv)}, nil
	}
	parentID := strings.TrimSpace(in.ParentConversationID)
	if parentID == "" {
		return nil, nil
	}
	query := &agconv.ConversationInput{
		ParentId: parentID,
		Has: &agconv.ConversationInputHas{
			ParentId:          true,
			IncludeTranscript: true,
		},
		IncludeTranscript: true,
	}
	if parentTurnID := strings.TrimSpace(in.ParentTurnID); parentTurnID != "" {
		query.ParentTurnId = parentTurnID
		query.Has.ParentTurnId = true
	}
	items, err := s.conv.GetConversations(ctx, (*apiconv.Input)(query))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	result := make([]StatusItem, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, s.statusItemFromConversation(item))
	}
	return result, nil
}

func lastAssistantState(transcript apiconv.Transcript) (string, string, bool, string, string, time.Time) {
	var lastNarration string
	var lastResponse string
	var hasFinal bool
	var lastMessageAt string
	var lastTurnStatus string
	var lastActivityAt time.Time
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		lastTurnStatus = strings.TrimSpace(turn.Status)
		if !turn.CreatedAt.IsZero() && turn.CreatedAt.After(lastActivityAt) {
			lastActivityAt = turn.CreatedAt
		}
		for _, msg := range turn.Message {
			if msg == nil || strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" {
				if msg != nil && !msg.CreatedAt.IsZero() && msg.CreatedAt.After(lastActivityAt) {
					lastActivityAt = msg.CreatedAt
				}
				continue
			}
			if strings.EqualFold(strings.TrimSpace(ptrString(msg.Mode)), "router") {
				continue
			}
			if !msg.CreatedAt.IsZero() {
				lastMessageAt = msg.CreatedAt.Format(time.RFC3339Nano)
				if msg.CreatedAt.After(lastActivityAt) {
					lastActivityAt = msg.CreatedAt
				}
			}
			if text := strings.TrimSpace(ptrString(msg.Narration)); text != "" {
				lastNarration = text
			} else if msg.Interim != 0 {
				if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
					lastNarration = text
				}
			}
			if msg.Interim == 0 {
				if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
					lastResponse = text
					hasFinal = true
				}
			}
		}
	}
	return lastNarration, lastResponse, hasFinal, lastMessageAt, lastTurnStatus, lastActivityAt
}

func normalizeChildConversationState(state childConversationState, transcript apiconv.Transcript) childConversationState {
	state.terminal = isTerminalChildStatus(state.status)
	failureSummary := latestChildFailureSummary(transcript)

	rawStatus := strings.ToLower(strings.TrimSpace(state.rawStatus))
	if rawStatus == "waiting_for_user" && strings.TrimSpace(failureSummary) != "" {
		state.status = "failed"
		state.terminal = true
		state.errorSummary = failureSummary
		state.hasFinalResponse = true
		state.lastAssistantNarration = ""
		state.lastAssistantResponse = "Child agent is blocked waiting for user input and cannot continue.\n" + failureSummary
		return state
	}

	if !state.terminal && childConversationTimedOut(state) {
		state.status = "failed"
		state.terminal = true
		timeoutLabel := "20 minutes"
		if strings.ToLower(strings.TrimSpace(state.rawStatus)) == "waiting_for_user" {
			timeoutLabel = "5 minutes"
		}
		state.errorSummary = "Child agent exceeded the maximum wait time of " + timeoutLabel + " without reaching a terminal state."
		state.hasFinalResponse = true
		state.lastAssistantNarration = ""
		state.lastAssistantResponse = "Child agent conversation " + strings.TrimSpace(state.conversationID) + " timed out after " + timeoutLabel + " without reaching a terminal state."
		if strings.TrimSpace(state.rawStatus) != "" {
			state.lastAssistantResponse += "\nLast known status: " + strings.TrimSpace(state.rawStatus) + "."
		}
		if strings.TrimSpace(failureSummary) != "" {
			state.lastAssistantResponse += "\n" + failureSummary
		}
		return state
	}

	if state.terminal && strings.TrimSpace(failureSummary) != "" {
		state.errorSummary = failureSummary
	}

	if state.terminal && !state.hasFinalResponse && strings.TrimSpace(failureSummary) != "" {
		state.hasFinalResponse = true
		state.lastAssistantNarration = ""
		state.lastAssistantResponse = failureSummary
	}
	return state
}

func isTerminalChildStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "completed", "success", "done", "failed", "error", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func childConversationTimedOut(state childConversationState) bool {
	if state.lastActivityAt.IsZero() {
		return false
	}
	timeout := DefaultChildStatusTimeout
	if strings.ToLower(strings.TrimSpace(state.rawStatus)) == "waiting_for_user" {
		timeout = DefaultWaitingForUserTimeout
	}
	return childStatusNow().Sub(state.lastActivityAt) > timeout
}

func latestChildFailureSummary(transcript apiconv.Transcript) string {
	for turnIdx := len(transcript) - 1; turnIdx >= 0; turnIdx-- {
		turn := transcript[turnIdx]
		if turn == nil {
			continue
		}
		if msg := strings.TrimSpace(ptrString(turn.ErrorMessage)); msg != "" {
			return "Turn error: " + msg
		}
		for msgIdx := len(turn.Message) - 1; msgIdx >= 0; msgIdx-- {
			msg := turn.Message[msgIdx]
			if msg == nil || strings.ToLower(strings.TrimSpace(ptrString(msg.Status))) != "failed" {
				continue
			}
			var parts []string
			if toolName := strings.TrimSpace(ptrString(msg.ToolName)); toolName != "" {
				parts = append(parts, "Tool "+toolName+" failed.")
			}
			if content := strings.TrimSpace(ptrString(msg.Content)); content != "" {
				parts = append(parts, "Error: "+content)
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
			return "A child tool call failed."
		}
	}
	return ""
}

func (s *Service) prepareLinkedRun(ctx context.Context, ri *RunInput, route string, waitForConversation bool) (linkedRun, error) {
	runCtx := linkedRun{parent: turnMetaFromContext(ctx)}
	agentID := effectiveRunAgentID(ri)
	if s.linker == nil || strings.TrimSpace(runCtx.parent.ConversationID) == "" {
		return runCtx, nil
	}
	scope := "new"
	if strings.EqualFold(strings.TrimSpace(route), "internal") {
		scope = s.agentConversationScopeForRun(ctx, ri)
	}
	logx.Infof("conversation", "agents.run %s scope agent_id=%q scope=%q", route, agentID, strings.TrimSpace(scope))
	if strings.EqualFold(strings.TrimSpace(route), "internal") {
		runCtx.childConversationID = s.resolveReusableChildConversation(ctx, agentID, runCtx.parent, scope, route)
	}
	// Only create a local child conversation for internal runs.
	// External A2A agents host their own conversation on a remote server;
	// creating a local stub would produce a dead linked-conversation card in the UI.
	if strings.TrimSpace(runCtx.childConversationID) == "" && strings.EqualFold(strings.TrimSpace(route), "internal") {
		childConversationID, err := s.createChildConversation(ctx, agentID, ri.Objective, runCtx.parent, route, waitForConversation)
		if err != nil {
			return runCtx, err
		}
		runCtx.childConversationID = childConversationID
	}
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		attachLinkedConversation(ctx, s.conv, runCtx.parent, runCtx.statusMessageID, runCtx.childConversationID)
	}
	statusToolName := "llm/agents:run"
	if ri != nil && ri.Async != nil && *ri.Async {
		statusToolName = "llm/agents:start"
	}
	logx.Infof("conversation", "agents.run %s status routing agent_id=%q child_convo=%q async_present=%v async_value=%v status_tool=%q", route, agentID, strings.TrimSpace(runCtx.childConversationID), ri != nil && ri.Async != nil, ri != nil && ri.Async != nil && *ri.Async, strings.TrimSpace(statusToolName))
	runCtx.statusToolName = statusToolName
	if !strings.EqualFold(statusToolName, "llm/agents:start") {
		logx.Infof("conversation", "agents.run %s creating status message agent_id=%q child_convo=%q status_tool=%q", route, agentID, strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(statusToolName))
		runCtx.statusMessageID = s.startRunStatus(ctx, runCtx.parent, runCtx.childConversationID, agentID, route, statusToolName)
	} else {
		logx.Infof("conversation", "agents.run %s skipping status message for async child agent_id=%q child_convo=%q", route, agentID, strings.TrimSpace(runCtx.childConversationID))
	}
	return runCtx, nil
}

func turnMetaFromContext(ctx context.Context) runtimerequestctx.TurnMeta {
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		return tm
	}
	return runtimerequestctx.TurnMeta{}
}

func (s *Service) agentConversationScope(ctx context.Context, agentID string) string {
	scope := "new"
	if s == nil || s.agent == nil || s.agent.Finder() == nil || strings.TrimSpace(agentID) == "" {
		return scope
	}
	if ag, err := s.agent.Finder().Find(ctx, strings.TrimSpace(agentID)); err == nil && ag != nil && ag.Profile != nil {
		v := strings.ToLower(strings.TrimSpace(ag.Profile.ConversationScope))
		if v == "parent" || v == "parentturn" || v == "new" {
			scope = v
		}
	}
	return scope
}

func (s *Service) agentConversationScopeForRun(ctx context.Context, ri *RunInput) string {
	if ri != nil && ri.Agent != nil && ri.Agent.Profile != nil {
		if v := strings.ToLower(strings.TrimSpace(ri.Agent.Profile.ConversationScope)); v == "parent" || v == "parentturn" || v == "new" {
			return v
		}
	}
	return s.agentConversationScope(ctx, effectiveRunAgentID(ri))
}

func (s *Service) resolveReusableChildConversation(ctx context.Context, agentID string, parent runtimerequestctx.TurnMeta, scope, route string) string {
	if s == nil || s.conv == nil || strings.TrimSpace(agentID) == "" || strings.TrimSpace(parent.ConversationID) == "" || scope == "new" {
		return ""
	}
	input := &agconv.ConversationInput{
		AgentId:  agentID,
		ParentId: parent.ConversationID,
		Has:      &agconv.ConversationInputHas{AgentId: true, ParentId: true},
	}
	if scope == "parentturn" {
		input.ParentTurnId = parent.TurnID
		input.Has.ParentTurnId = true
	}
	logx.Infof("conversation", "agents.run %s reuse lookup agent_id=%q parent_convo=%q parent_turn=%q scope=%q", route, strings.TrimSpace(agentID), strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(scope))
	if cid := s.lookupReusableChildConversation(ctx, input); strings.TrimSpace(cid) != "" {
		logx.Infof("conversation", "agents.run %s reuse hit agent_id=%q child_convo=%q", route, strings.TrimSpace(agentID), strings.TrimSpace(cid))
		return cid
	}
	return ""
}

func (s *Service) createChildConversation(ctx context.Context, agentID, objective string, parent runtimerequestctx.TurnMeta, route string, waitForConversation bool) (string, error) {
	if s == nil || s.linker == nil || strings.TrimSpace(parent.ConversationID) == "" {
		return "", nil
	}
	cid, err := s.linker.CreateLinkedConversation(ctx, parent, false, nil)
	if err != nil {
		logx.Errorf("conversation", "agents.run %s create child error parent_convo=%q err=%v", route, strings.TrimSpace(parent.ConversationID), err)
		return "", nil
	}
	logx.Infof("conversation", "agents.run %s created child_convo=%q parent_convo=%q", route, strings.TrimSpace(cid), strings.TrimSpace(parent.ConversationID))
	if strings.EqualFold(strings.TrimSpace(route), "internal") {
		s.assignConversationAgent(ctx, cid, agentID, route)
	}
	if waitForConversation {
		if err := s.waitForConversation(ctx, cid); err != nil {
			logx.Errorf("conversation", "agents.run %s wait child error child_convo=%q err=%v", route, strings.TrimSpace(cid), err)
			return "", err
		}
	}
	return cid, nil
}

func (s *Service) assignConversationAgent(ctx context.Context, conversationID, agentID, route string) {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(agentID) == "" {
		return
	}
	upd := convw.Conversation{Has: &convw.ConversationHas{}}
	upd.SetId(strings.TrimSpace(conversationID))
	upd.SetAgentId(strings.TrimSpace(agentID))
	if err := s.conv.PatchConversations(ctx, (*apiconv.MutableConversation)(&upd)); err != nil {
		logx.Errorf("conversation", "agents.run %s set agent error child_convo=%q agent_id=%q err=%v", route, strings.TrimSpace(conversationID), strings.TrimSpace(agentID), err)
	}
}

func (s *Service) startRunStatus(ctx context.Context, parent runtimerequestctx.TurnMeta, childConversationID, childAgentID, route, toolName string) string {
	if s == nil || s.status == nil || strings.TrimSpace(parent.ConversationID) == "" {
		return ""
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		toolName = "llm/agents:run"
	}
	mid, err := s.status.Start(ctx, parent, toolName, "assistant", "tool", "exec")
	if err != nil {
		logx.Errorf("conversation", "agents.run %s status start error parent_convo=%q err=%v", route, strings.TrimSpace(parent.ConversationID), err)
		return ""
	}
	label := strings.TrimSpace(childAgentID)
	if label == "" {
		label = "linked agent"
	}
	preview := "Running " + label + "."
	if err := s.status.Update(ctx, parent, mid, preview); err != nil {
		logx.Warnf("conversation", "agents.run %s status update error parent_convo=%q message_id=%q err=%v", route, strings.TrimSpace(parent.ConversationID), strings.TrimSpace(mid), err)
	}
	attachLinkedConversation(ctx, s.conv, parent, mid, childConversationID)
	if s.linker != nil {
		eventToolCallID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
		if eventToolCallID == "" {
			eventToolCallID = mid
		}
		s.linker.EmitLinkedConversationAttached(ctx, parent, childConversationID, eventToolCallID, childAgentID, "")
	}
	logx.Infof("conversation", "agents.run %s status start parent_convo=%q message_id=%q", route, strings.TrimSpace(parent.ConversationID), strings.TrimSpace(mid))
	return mid
}

func (s *Service) finalizeRunStatus(ctx context.Context, runCtx linkedRun, status string) {
	if s == nil || s.status == nil || strings.TrimSpace(runCtx.statusMessageID) == "" || strings.TrimSpace(runCtx.parent.ConversationID) == "" {
		return
	}
	_ = s.status.Finalize(ctx, runCtx.parent, runCtx.statusMessageID, strings.TrimSpace(status), "")
}

func (s *Service) surfaceAsyncCompletion(ctx context.Context, runCtx linkedRun, agentID string, result childRunResult) {
	if s == nil || strings.TrimSpace(runCtx.parent.ConversationID) == "" {
		return
	}
	if strings.TrimSpace(runCtx.childConversationID) == "" {
		return
	}
	attachLinkedConversation(ctx, s.conv, runCtx.parent, runCtx.statusMessageID, runCtx.childConversationID)
	if s.agent != nil && s.conv != nil {
		status := strings.TrimSpace(result.status)
		message := strings.TrimSpace(result.answer)
		messageKind := ""
		errText := ""
		if state, ok := s.childConversationState(context.Background(), runCtx.childConversationID); ok {
			status = strings.TrimSpace(state.status)
			if text := strings.TrimSpace(state.lastAssistantResponse); text != "" {
				message = text
				messageKind = "response"
			} else if text := strings.TrimSpace(state.lastAssistantNarration); text != "" {
				message = text
				messageKind = "preamble"
			}
			errText = strings.TrimSpace(state.errorSummary)
		} else if result.err != nil {
			errText = strings.TrimSpace(result.err.Error())
		}
		if status == "" {
			if errText != "" {
				status = "failed"
			} else {
				status = "succeeded"
			}
		}
		if message == "" {
			message = status
		}
		if provider, ok := any(s.agent).(interface{ AsyncManager() *asynccfg.Manager }); ok && provider != nil {
			mgr := provider.AsyncManager()
			if mgr == nil {
				goto linkedEvent
			}
			if rec, _ := mgr.Update(context.Background(), asynccfg.UpdateInput{
				ID:          runCtx.childConversationID,
				Status:      status,
				Message:     message,
				MessageKind: messageKind,
				Error:       errText,
			}); rec != nil {
				toolexec.PatchAsyncToolPersistence(context.Background(), s.conv, rec, "", &asynccfg.Extracted{
					Status:      status,
					Message:     message,
					MessageKind: messageKind,
					Error:       errText,
				})
			}
		}
	}
linkedEvent:
	if s.linker != nil {
		s.linker.EmitLinkedConversationAttached(ctx, runCtx.parent, runCtx.childConversationID, "", agentID, "")
	}
}

// parentConversationModel returns the default model from the parent
// conversation, if available. This allows child agents to inherit the
// user-selected model instead of falling back to a system default.
func (s *Service) parentConversationModel(ctx context.Context) string {
	if s == nil || s.conv == nil {
		return ""
	}
	parentConvID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if parentConvID == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, parentConvID)
	if err != nil || conv == nil || conv.DefaultModel == nil {
		return ""
	}
	return strings.TrimSpace(*conv.DefaultModel)
}
