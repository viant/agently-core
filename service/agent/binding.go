package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/workspace"
)

func (s *Service) BuildBinding(ctx context.Context, input *QueryInput) (*prompt.Binding, error) {
	start := time.Now()
	convoID := ""
	if input != nil {
		convoID = strings.TrimSpace(input.ConversationID)
	}
	debugf("agent.BuildBinding start convo=%q", convoID)
	b := &prompt.Binding{}
	if input.Agent != nil {
		b.Model = input.Agent.Model
	}
	if strings.TrimSpace(input.ModelOverride) != "" {
		b.Model = strings.TrimSpace(input.ModelOverride)
	}
	// Fetch conversation transcript once and reuse; bubble up errors
	if s.conversation == nil {
		debugf("agent.BuildBinding error convo=%q elapsed=%s err=%v", convoID, time.Since(start).String(), "conversation API not configured")
		return nil, fmt.Errorf("conversation API not configured")
	}
	fetchStart := time.Now()
	debugf("agent.BuildBinding fetchConversation start convo=%q", convoID)
	conv, err := s.fetchConversationWithRetry(ctx, input.ConversationID, apiconv.WithIncludeToolCall(true))
	if err != nil {
		debugf("agent.BuildBinding fetchConversation error convo=%q elapsed=%s err=%v", convoID, time.Since(fetchStart).String(), err)
		return nil, err
	}
	debugf("agent.BuildBinding fetchConversation ok convo=%q elapsed=%s", convoID, time.Since(fetchStart).String())
	if conv == nil {
		debugf("agent.BuildBinding error convo=%q elapsed=%s err=%v", convoID, time.Since(start).String(), "conversation not found")
		return nil, fmt.Errorf("conversation not found: %s", strings.TrimSpace(input.ConversationID))
	}
	ctx = applySchedulerDiscoveryMode(ctx, conv)

	// Compute effective preview limit using service defaults only
	histStart := time.Now()
	debugf("agent.BuildBinding buildHistory start convo=%q", convoID)
	histResult, err := s.buildHistoryWithLimit(ctx, conv.GetTranscript(), input)
	if err != nil {
		debugf("agent.BuildBinding buildHistory error convo=%q elapsed=%s err=%v", convoID, time.Since(histStart).String(), err)
		return nil, err
	}
	debugf("agent.BuildBinding buildHistory ok convo=%q elapsed=%s overflow=%t elicitation=%d", convoID, time.Since(histStart).String(), histResult.Overflow, len(histResult.Elicitation))
	b.History = histResult.History
	// Align History.CurrentTurnID with the in-flight turn when available
	if tm, ok := memory.TurnMetaFromContext(ctx); ok {
		b.History.CurrentTurnID = strings.TrimSpace(tm.TurnID)
	}
	// Attach current-turn elicitation messages to History.Current so
	// they can participate in a unified, chronological view of the
	// in-flight turn.
	if len(histResult.Elicitation) > 0 {
		appendCurrentMessages(&b.History, histResult.Elicitation...)
	}
	// Populate History.LastResponse using the last assistant message in transcript
	if conv != nil {
		tr := conv.GetTranscript()
		if last := tr.LastAssistantMessageWithModelCall(); last != nil {
			trace := &prompt.Trace{At: last.CreatedAt, Kind: prompt.KindResponse}
			if last.ModelCall != nil && last.ModelCall.TraceId != nil {
				if id := strings.TrimSpace(*last.ModelCall.TraceId); id != "" {
					trace.ID = id
				}
			}
			b.History.LastResponse = trace
			// Build History.Traces map: resp, opid and content keys
			b.History.Traces = s.buildTraces(tr)

		}
	}
	// Elicitation payload is merged into binding.Context later (after cloning input.Context)
	// to avoid mutating caller-supplied maps and to keep a single authoritative merge point.
	if histResult.Overflow {
		b.Flags.HasMessageOverflow = true
	}
	if histResult.MaxOverflowBytes > 0 {
		b.Flags.MaxOverflowBytes = histResult.MaxOverflowBytes
	}

	b.Task = s.buildTaskBinding(input)

	toolsStart := time.Now()
	debugf("agent.BuildBinding buildToolSignatures start convo=%q", convoID)
	b.Tools.Signatures, err = s.buildToolSignatures(ctx, input)
	if err != nil {
		debugf("agent.BuildBinding buildToolSignatures error convo=%q elapsed=%s err=%v", convoID, time.Since(toolsStart).String(), err)
		return nil, err
	}
	debugf("agent.BuildBinding buildToolSignatures ok convo=%q elapsed=%s tools=%d", convoID, time.Since(toolsStart).String(), len(b.Tools.Signatures))

	// Tool executions exposure: default "turn"; allow QueryInput override; then agent setting.
	exposure := agent.ToolCallExposure("turn")
	if input.ToolCallExposure != nil && strings.TrimSpace(string(*input.ToolCallExposure)) != "" {
		exposure = *input.ToolCallExposure
	} else if input.Agent != nil && strings.TrimSpace(string(input.Agent.Tool.CallExposure)) != "" {
		exposure = input.Agent.Tool.CallExposure
	}

	b.History.ToolExposure = string(exposure)

	execStart := time.Now()
	debugf("agent.BuildBinding buildToolExecutions start convo=%q exposure=%q", convoID, string(exposure))
	execResult, err := s.buildToolExecutions(ctx, input, conv, exposure)
	if err != nil {
		debugf("agent.BuildBinding buildToolExecutions error convo=%q elapsed=%s err=%v", convoID, time.Since(execStart).String(), err)
		return nil, err
	}
	debugf("agent.BuildBinding buildToolExecutions ok convo=%q elapsed=%s overflow=%t", convoID, time.Since(execStart).String(), execResult.Overflow)

	// Drive overflow-based helper exposure via binding flag
	if execResult.Overflow {
		b.Flags.HasMessageOverflow = true
	}
	if execResult.MaxOverflowBytes > 0 && execResult.MaxOverflowBytes > b.Flags.MaxOverflowBytes {
		b.Flags.MaxOverflowBytes = execResult.MaxOverflowBytes
	}

	// If any tool call in the current turn overflowed, expose callToolResult tools
	turnMeta, ok := memory.TurnMetaFromContext(ctx)
	if ok && strings.TrimSpace(turnMeta.TurnID) != "" {
		var current *apiconv.Turn
		for _, t := range conv.GetTranscript() {
			if t != nil && t.Id == turnMeta.TurnID {
				current = t
				break
			}
		}
		s.handleOverflow(ctx, input, current, b)
		// Allow tool-use if we appended any
		if len(b.Tools.Signatures) > 0 && b.Model != "" {
			b.Flags.CanUseTool = s.llm.ModelImplements(ctx, b.Model, base.CanUseTools)
		}

	}

	// Append internal tools needed for continuation flows (no duplicates)
	s.ensureInternalToolsIfNeeded(ctx, input, b)

	docsStart := time.Now()
	debugf("agent.BuildBinding buildDocuments start convo=%q", convoID)
	docs, err := s.buildDocumentsBinding(ctx, input, false)
	if err != nil {
		debugf("agent.BuildBinding buildDocuments error convo=%q elapsed=%s err=%v", convoID, time.Since(docsStart).String(), err)
		return nil, err
	}

	debugf("agent.BuildBinding buildDocuments ok convo=%q elapsed=%s docs=%d", convoID, time.Since(docsStart).String(), len(docs.Items))
	b.Documents = docs
	// Normalize user doc URIs by trimming workspace root for stable display
	s.normalizeDocURIs(&b.Documents, workspace.Root())
	// Attach non-text user documents as binary attachments (e.g., PDFs, images)
	s.attachNonTextUserDocs(ctx, b)

	sysDocsStart := time.Now()
	debugf("agent.BuildBinding buildSystemDocuments start convo=%q", convoID)
	b.SystemDocuments, err = s.buildDocumentsBinding(ctx, input, true)
	if err != nil {
		debugf("agent.BuildBinding buildSystemDocuments error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	debugf("agent.BuildBinding buildSystemDocuments ok convo=%q elapsed=%s docs=%d", convoID, time.Since(sysDocsStart).String(), len(b.SystemDocuments.Items))
	s.appendTranscriptSystemDocs(conv.GetTranscript(), b)
	if err := s.appendToolPlaybooks(ctx, b.Tools.Signatures, &b.SystemDocuments); err != nil {
		debugf("agent.BuildBinding appendToolPlaybooks error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	s.appendAgentDirectoryDoc(ctx, input, &b.SystemDocuments)
	// Normalize system doc URIs similarly (even if not rendered now)
	s.normalizeDocURIs(&b.SystemDocuments, workspace.Root())
	b.Context = input.Context

	// Expose tool availability flags for templates (dynamic tool selection).
	// Avoid mutating input.Context directly by working on a copy.
	b.Context = cloneContextMap(b.Context)
	mergeElicitationPayloadIntoContext(b.History, &b.Context)
	s.applyDelegationContext(input, b)

	debugf("agent.BuildBinding ok convo=%q elapsed=%s history_msgs=%d sys_docs=%d docs=%d tools=%d", convoID, time.Since(start).String(), len(b.History.Messages), len(b.SystemDocuments.Items), len(b.Documents.Items), len(b.Tools.Signatures))
	return b, nil
}

func cloneContextMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Service) applyDelegationContext(input *QueryInput, b *prompt.Binding) {
	if input == nil || b == nil || input.Agent == nil {
		debugf("delegation.context skip missing input/binding/agent")
		return
	}
	if input.Agent.Delegation == nil || !input.Agent.Delegation.Enabled {
		debugf("delegation.context disabled agent_id=%q", strings.TrimSpace(input.Agent.ID))
		return
	}
	if b.Context == nil {
		b.Context = map[string]interface{}{}
	}
	maxDepth := input.Agent.Delegation.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	b.Context["DelegationEnabled"] = true
	b.Context["DelegationMaxDepth"] = maxDepth
	if strings.TrimSpace(input.Agent.ID) != "" {
		b.Context["DelegationSelfID"] = strings.TrimSpace(input.Agent.ID)
	}
	// Seed a depth map if missing so templates can reference it.
	if _, ok := b.Context["DelegationDepths"]; !ok {
		b.Context["DelegationDepths"] = map[string]interface{}{}
	}
	debugf("delegation.context enabled agent_id=%q maxDepth=%d", strings.TrimSpace(input.Agent.ID), maxDepth)
}
