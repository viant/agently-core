package agents

import (
	"context"
	"strings"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/internal/textutil"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	convw "github.com/viant/agently-core/pkg/agently/conversation/write"
	toolpol "github.com/viant/agently-core/protocol/tool"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/memory"
	agentsvc "github.com/viant/agently-core/service/agent"
)

type linkedRun struct {
	parent              memory.TurnMeta
	childConversationID string
	statusMessageID     string
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
	qi := &agentsvc.QueryInput{AgentID: ri.AgentID, Query: ri.Objective, Context: childContext}
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
	qi.ToolsAllowed = []string{}
	if ri.ModelPreferences != nil && (qi.Agent == nil || strings.TrimSpace(qi.Agent.ModelSelection.Model) == "") {
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
	childCtx := toolpol.WithPolicy(ctx, nil)
	debugf("agents.run internal invoke agent_id=%q child_convo=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID))
	if err := s.agent.Query(childCtx, qi, qo); err != nil {
		errorf("agents.run internal error agent_id=%q child_convo=%q err=%v", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), err)
		s.finalizeRunStatus(ctx, runCtx, "failed")
		return err
	}
	debugf("agents.run internal ok agent_id=%q child_convo=%q message_id=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(runCtx.childConversationID), strings.TrimSpace(qo.MessageID))
	ro.Answer = qo.Content
	ro.Status = "succeeded"
	if strings.TrimSpace(qo.ConversationID) != "" {
		ro.ConversationID = qo.ConversationID
	}
	if ro.ConversationID == "" {
		ro.ConversationID = convID
	}
	ro.MessageID = qo.MessageID
	s.finalizeRunStatus(ctx, runCtx, "succeeded")
	debugf("agents.run done convo=%q agent_id=%q status=%q", strings.TrimSpace(ro.ConversationID), strings.TrimSpace(ri.AgentID), strings.TrimSpace(ro.Status))
	return nil
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
	s.addLinkPreview(ctx, parent, cid, objective, route)
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

func (s *Service) addLinkPreview(ctx context.Context, parent memory.TurnMeta, childConversationID, objective, route string) {
	if s == nil || s.linker == nil || strings.TrimSpace(childConversationID) == "" || strings.TrimSpace(parent.ConversationID) == "" {
		return
	}
	preview := textutil.RuneTruncate(strings.TrimSpace(objective), 512)
	if err := s.linker.AddLinkMessage(ctx, parent, childConversationID, "assistant", "tool", "exec", preview); err != nil {
		errorf("agents.run %s link message error child_convo=%q err=%v", route, strings.TrimSpace(childConversationID), err)
	}
}

func (s *Service) startRunStatus(ctx context.Context, parent memory.TurnMeta, childConversationID, route string) string {
	if s == nil || s.status == nil || strings.TrimSpace(parent.ConversationID) == "" {
		return ""
	}
	mid, err := s.status.Start(ctx, parent, "llm/agents-run", "assistant", "tool", "exec")
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
