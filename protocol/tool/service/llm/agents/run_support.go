package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	authctx "github.com/viant/agently-core/internal/auth"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	toolpol "github.com/viant/agently-core/protocol/tool"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/memory"
	agentsvc "github.com/viant/agently-core/service/agent"
	coreauth "github.com/viant/agently-core/service/auth"
)

type linkedRun struct {
	parent              memory.TurnMeta
	childConversationID string
	statusMessageID     string
}

type childRunResult struct {
	answer         string
	status         string
	conversationID string
	messageID      string
	err            error
}

type childConversationState struct {
	conversationID        string
	parentConversationID  string
	parentTurnID          string
	agentID               string
	status                string
	createdAt             string
	updatedAt             string
	lastAssistantPreamble string
	lastAssistantResponse string
	hasFinalResponse      bool
	lastMessageAt         string
}

func (s *Service) tryExternalRun(ctx context.Context, ri *RunInput, ro *RunOutput, intended string) (bool, error) {
	runCtx, err := s.prepareLinkedRun(ctx, ri, "external", false)
	if err != nil {
		return true, err
	}
	extCtx := ctx
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		extCtx = memory.WithConversationID(ctx, runCtx.childConversationID)
		ro.ConversationID = runCtx.childConversationID
	}
	debugf("agents.run external invoke agent_id=%q child_convo=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID))
	ans, st, taskID, ctxID, streamSupp, warns, err := s.runExternal(extCtx, ri.AgentID, ri.Objective, ri.Context)
	if err != nil {
		errorf("agents.run external error agent_id=%q child_convo=%q err=%v", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), err)
		s.finalizeRunStatus(ctx, runCtx, "failed")
		if intended == "external" {
			return true, err
		}
		return false, nil
	}
	if taskID == "" && st == "" {
		return false, nil
	}
	debugf("agents.run external ok agent_id=%q child_convo=%q status=%q task_id=%q context_id=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(st), strings.TrimSpace(taskID), strings.TrimSpace(ctxID))
	ro.Answer = ans
	ro.Status = st
	ro.TaskID = taskID
	if ro.ConversationID == "" {
		ro.ConversationID = strings.TrimSpace(memory.ConversationIDFromContext(extCtx))
	}
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
const DefaultChildAgentTimeout = 15 * time.Minute

func (s *Service) runInternal(ctx context.Context, ri *RunInput, ro *RunOutput, convID string, depth int) error {
	if s.agent == nil {
		errorf("agents.run internal error: agent runtime not configured")
		return svc.NewMethodNotFoundError("agent runtime not configured")
	}
	runCtx, err := s.prepareLinkedRun(ctx, ri, "internal", true)
	if err != nil {
		return err
	}
	childContext := inheritDelegatedContext(ctx, ri.Context)
	qi := &agentsvc.QueryInput{AgentID: ri.AgentID, Query: normalizedDelegatedObjective(ri), Context: childContext}
	if s.agent != nil && s.agent.Finder() != nil && strings.TrimSpace(ri.AgentID) != "" {
		if ag, err := s.agent.Finder().Find(ctx, strings.TrimSpace(ri.AgentID)); err == nil && ag != nil {
			qi.Agent = ag
		}
	}
	if strings.TrimSpace(ri.AgentID) != "" {
		childContext = setDelegationDepth(childContext, strings.TrimSpace(ri.AgentID), depth+1)
		ri.Context = childContext
		qi.Context = childContext
	}
	qi.ToolsAllowed = delegatedToolAllowList(ri)
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
	debugf("agents.run done convo=%q agent_id=%q status=%q", strings.TrimSpace(ro.ConversationID), strings.TrimSpace(ri.AgentID), strings.TrimSpace(ro.Status))
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
		memory.ModelMessageIDKey, "",
	)
	// Child runs must not inherit the parent's conversation/turn context.
	// Agent.Query performs pre-turn work before it seeds a new turn, so bind
	// the child conversation id here and reset turn metadata up front.
	if strings.TrimSpace(runCtx.childConversationID) != "" {
		childCtx = memory.WithConversationID(childCtx, strings.TrimSpace(runCtx.childConversationID))
	}
	childCtx = memory.WithTurnMeta(childCtx, memory.TurnMeta{})
	childCtx = inheritDelegatedAuthContext(childCtx, ctx)
	childTimeout := s.ChildTimeout
	if childTimeout <= 0 {
		childTimeout = DefaultChildAgentTimeout
	}
	childCtx, childCancel := context.WithTimeout(childCtx, childTimeout)
	defer childCancel()
	debugf("agents.run internal invoke agent_id=%q child_convo=%q timeout=%s", strings.TrimSpace(qi.AgentID), strings.TrimSpace(runCtx.childConversationID), childTimeout)
	if err := s.agent.Query(childCtx, qi, qo); err != nil {
		errorf("agents.run internal error agent_id=%q child_convo=%q err=%v", strings.TrimSpace(qi.AgentID), strings.TrimSpace(runCtx.childConversationID), err)
		return s.resolveChildRunError(ctx, runCtx, qo, err)
	}
	debugf("agents.run internal ok agent_id=%q child_convo=%q message_id=%q", strings.TrimSpace(qi.AgentID), strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(qo.MessageID))
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

func delegatedToolAllowList(ri *RunInput) []string {
	if ri == nil {
		return []string{}
	}
	if !shouldUseRepoAnalysisRewrite(ri) {
		return []string{}
	}
	return []string{
		"resources:list",
		"resources-list",
		"resources:read",
		"resources-read",
		"resources:grepFiles",
		"resources-grepFiles",
		"resources:roots",
		"resources-roots",
		"resources:match",
		"resources-match",
		"resources:matchDocuments",
		"resources-matchDocuments",
		"system/exec:execute",
		"system_exec-execute",
		"system/os:getEnv",
		"system_os-getEnv",
		"message:show",
		"message-show",
		"internal/message:show",
		"internal_message-show",
		"message:summarize",
		"message-summarize",
		"internal/message:summarize",
		"internal_message-summarize",
		"message:match",
		"message-match",
		"internal/message:match",
		"internal_message-match",
	}
}

func looksLikeRepoAnalysisObjective(objective string) bool {
	lower := strings.ToLower(strings.TrimSpace(objective))
	if lower == "" {
		return false
	}
	if !(strings.Contains(lower, "analyze") ||
		strings.Contains(lower, "analyse") ||
		strings.Contains(lower, "inspect") ||
		strings.Contains(lower, "review") ||
		strings.Contains(lower, "summarize") ||
		strings.Contains(lower, "summarise") ||
		strings.Contains(lower, "explain")) {
		return false
	}
	return strings.Contains(lower, "/") ||
		strings.Contains(lower, "repo") ||
		strings.Contains(lower, "repository") ||
		strings.Contains(lower, "codebase") ||
		strings.Contains(lower, "project") ||
		strings.Contains(lower, "directory")
}

func normalizedDelegatedObjective(ri *RunInput) string {
	if ri == nil {
		return ""
	}
	objective := strings.TrimSpace(ri.Objective)
	if !shouldUseRepoAnalysisRewrite(ri) {
		return objective
	}
	workdir := strings.TrimSpace(stringValue(ri.Context, "resolvedWorkdir"))
	if workdir == "" {
		workdir = strings.TrimSpace(stringValue(ri.Context, "workdir"))
	}
	target := workdir
	if target == "" {
		target = objective
	}
	return strings.TrimSpace(
		"Analyze the repository at " + target + ". " +
			"Use at most one `resources-list` call on the repo root, then 1-3 targeted `resources-grepFiles` or `resources-read` calls to answer the task. " +
			"Do not start another broad discovery round after you already know the repo layout. " +
			"Return a focused summary covering main modules or entrypoints, any MCP-related implementation patterns you found, and the most important gaps or risks. " +
			"Once you have enough evidence for that summary, stop tool use and answer.",
	)
}

func shouldUseRepoAnalysisRewrite(ri *RunInput) bool {
	if ri == nil || ri.Context == nil {
		return false
	}
	return boolValue(ri.Context, "repoAnalysis") ||
		boolValue(ri.Context, "repo_analysis") ||
		strings.EqualFold(strings.TrimSpace(stringValue(ri.Context, "delegatedMode")), "repo_analysis")
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
	if !ok || !isSuccessfulStatus(state.status) || strings.TrimSpace(state.lastAssistantResponse) == "" {
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
	if !conv.CreatedAt.IsZero() {
		state.createdAt = conv.CreatedAt.Format(time.RFC3339Nano)
	}
	if conv.UpdatedAt != nil && !conv.UpdatedAt.IsZero() {
		state.updatedAt = conv.UpdatedAt.Format(time.RFC3339Nano)
	}
	preamble, response, hasFinal, lastMessageAt, lastTurnStatus := lastAssistantState(conv.GetTranscript())
	if state.status == "" {
		state.status = lastTurnStatus
	}
	state.lastAssistantPreamble = preamble
	state.lastAssistantResponse = response
	state.hasFinalResponse = hasFinal
	state.lastMessageAt = lastMessageAt
	return state, true
}

func (s *Service) statusItemFromConversation(conv *apiconv.Conversation) StatusItem {
	if conv == nil {
		return StatusItem{}
	}
	state, ok := s.childConversationState(context.Background(), strings.TrimSpace(conv.Id))
	if !ok {
		return StatusItem{}
	}
	return StatusItem{
		ConversationID:        state.conversationID,
		ParentConversationID:  state.parentConversationID,
		ParentTurnID:          state.parentTurnID,
		AgentID:               state.agentID,
		Status:                state.status,
		CreatedAt:             state.createdAt,
		UpdatedAt:             state.updatedAt,
		LastAssistantPreamble: state.lastAssistantPreamble,
		LastAssistantResponse: state.lastAssistantResponse,
		HasFinalResponse:      state.hasFinalResponse,
		LastMessageAt:         state.lastMessageAt,
	}
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
		if text := strings.TrimSpace(ptrString(msg.Preamble)); text != "" {
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
	so.Items = items
	return nil
}

func (s *Service) collectStatusItems(ctx context.Context, in *StatusInput) ([]StatusItem, error) {
	if s == nil || s.conv == nil || in == nil {
		return nil, nil
	}
	if convID := strings.TrimSpace(in.ConversationID); convID != "" {
		conv, err := s.conv.GetConversation(ctx, convID, apiconv.WithIncludeTranscript(true))
		if err != nil {
			return nil, err
		}
		if conv == nil {
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

func lastAssistantState(transcript apiconv.Transcript) (string, string, bool, string, string) {
	var lastPreamble string
	var lastResponse string
	var hasFinal bool
	var lastMessageAt string
	var lastTurnStatus string
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		lastTurnStatus = strings.TrimSpace(turn.Status)
		for _, msg := range turn.Message {
			if msg == nil || strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" {
				continue
			}
			if !msg.CreatedAt.IsZero() {
				lastMessageAt = msg.CreatedAt.Format(time.RFC3339Nano)
			}
			if text := strings.TrimSpace(ptrString(msg.Preamble)); text != "" {
				lastPreamble = text
			} else if msg.Interim != 0 {
				if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
					lastPreamble = text
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
	return lastPreamble, lastResponse, hasFinal, lastMessageAt, lastTurnStatus
}

func (s *Service) prepareLinkedRun(ctx context.Context, ri *RunInput, route string, waitForConversation bool) (linkedRun, error) {
	runCtx := linkedRun{parent: turnMetaFromContext(ctx)}
	if s.linker == nil || strings.TrimSpace(runCtx.parent.ConversationID) == "" {
		return runCtx, nil
	}
	scope := s.agentConversationScope(ctx, strings.TrimSpace(ri.AgentID))
	debugf("agents.run %s scope agent_id=%q scope=%q", route, strings.TrimSpace(ri.AgentID), strings.TrimSpace(scope))
	runCtx.childConversationID = s.resolveReusableChildConversation(ctx, ri.AgentID, runCtx.parent, scope, route)
	if strings.TrimSpace(runCtx.childConversationID) == "" {
		childConversationID, err := s.createChildConversation(ctx, ri.AgentID, ri.Objective, runCtx.parent, route, waitForConversation)
		if err != nil {
			return runCtx, err
		}
		runCtx.childConversationID = childConversationID
	}
	runCtx.statusMessageID = s.startRunStatus(ctx, runCtx.parent, runCtx.childConversationID, route)
	return runCtx, nil
}

func turnMetaFromContext(ctx context.Context) memory.TurnMeta {
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		return tm
	}
	return memory.TurnMeta{}
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

func (s *Service) resolveReusableChildConversation(ctx context.Context, agentID string, parent memory.TurnMeta, scope, route string) string {
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
	debugf("agents.run %s reuse lookup agent_id=%q parent_convo=%q parent_turn=%q scope=%q", route, strings.TrimSpace(agentID), strings.TrimSpace(parent.ConversationID), strings.TrimSpace(parent.TurnID), strings.TrimSpace(scope))
	if cid := s.lookupReusableChildConversation(ctx, input); strings.TrimSpace(cid) != "" {
		debugf("agents.run %s reuse hit agent_id=%q child_convo=%q", route, strings.TrimSpace(agentID), strings.TrimSpace(cid))
		return cid
	}
	return ""
}

func (s *Service) createChildConversation(ctx context.Context, agentID, objective string, parent memory.TurnMeta, route string, waitForConversation bool) (string, error) {
	if s == nil || s.linker == nil || strings.TrimSpace(parent.ConversationID) == "" {
		return "", nil
	}
	cid, err := s.linker.CreateLinkedConversation(ctx, parent, false, nil)
	if err != nil {
		errorf("agents.run %s create child error parent_convo=%q err=%v", route, strings.TrimSpace(parent.ConversationID), err)
		return "", nil
	}
	debugf("agents.run %s created child_convo=%q parent_convo=%q", route, strings.TrimSpace(cid), strings.TrimSpace(parent.ConversationID))
	s.assignConversationAgent(ctx, cid, agentID, route)
	if waitForConversation {
		if err := s.waitForConversation(ctx, cid); err != nil {
			errorf("agents.run %s wait child error child_convo=%q err=%v", route, strings.TrimSpace(cid), err)
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
		errorf("agents.run %s set agent error child_convo=%q agent_id=%q err=%v", route, strings.TrimSpace(conversationID), strings.TrimSpace(agentID), err)
	}
}

func (s *Service) startRunStatus(ctx context.Context, parent memory.TurnMeta, childConversationID, route string) string {
	if s == nil || s.status == nil || strings.TrimSpace(parent.ConversationID) == "" {
		return ""
	}
	mid, err := s.status.Start(ctx, parent, "llm/agents:run", "assistant", "tool", "exec")
	if err != nil {
		errorf("agents.run %s status start error parent_convo=%q err=%v", route, strings.TrimSpace(parent.ConversationID), err)
		return ""
	}
	attachLinkedConversation(ctx, s.conv, parent, mid, childConversationID)
	debugf("agents.run %s status start parent_convo=%q message_id=%q", route, strings.TrimSpace(parent.ConversationID), strings.TrimSpace(mid))
	return mid
}

func (s *Service) finalizeRunStatus(ctx context.Context, runCtx linkedRun, status string) {
	if s == nil || s.status == nil || strings.TrimSpace(runCtx.statusMessageID) == "" || strings.TrimSpace(runCtx.parent.ConversationID) == "" {
		return
	}
	_ = s.status.Finalize(ctx, runCtx.parent, runCtx.statusMessageID, strings.TrimSpace(status), "")
}

// parentConversationModel returns the default model from the parent
// conversation, if available. This allows child agents to inherit the
// user-selected model instead of falling back to a system default.
func (s *Service) parentConversationModel(ctx context.Context) string {
	if s == nil || s.conv == nil {
		return ""
	}
	parentConvID := strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	if parentConvID == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, parentConvID)
	if err != nil || conv == nil || conv.DefaultModel == nil {
		return ""
	}
	return strings.TrimSpace(*conv.DefaultModel)
}
