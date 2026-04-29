package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/logx"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	base "github.com/viant/agently-core/genai/llm/provider/base"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	templatesvc "github.com/viant/agently-core/protocol/tool/service/template"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/workspace"
)

func (s *Service) BuildBinding(ctx context.Context, input *QueryInput) (*binding.Binding, error) {
	start := time.Now()
	convoID := ""
	if input != nil {
		convoID = strings.TrimSpace(input.ConversationID)
	}
	logx.Infof("conversation", "agent.BuildBinding start convo=%q", convoID)
	b := &binding.Binding{}
	if input.Agent != nil {
		b.Model = input.Agent.Model
	}
	if strings.TrimSpace(input.ModelOverride) != "" {
		b.Model = strings.TrimSpace(input.ModelOverride)
	}
	// Fetch conversation transcript once and reuse; bubble up errors
	if s.conversation == nil {
		logx.Infof("conversation", "agent.BuildBinding error convo=%q elapsed=%s err=%v", convoID, time.Since(start).String(), "conversation API not configured")
		return nil, fmt.Errorf("conversation API not configured")
	}
	fetchStart := time.Now()
	logx.Infof("conversation", "agent.BuildBinding fetchConversation start convo=%q", convoID)
	conv, err := s.bindingConversation(ctx, input)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding fetchConversation error convo=%q elapsed=%s err=%v", convoID, time.Since(fetchStart).String(), err)
		return nil, err
	}
	logx.Infof("conversation", "agent.BuildBinding fetchConversation ok convo=%q elapsed=%s", convoID, time.Since(fetchStart).String())
	if conv == nil {
		logx.Infof("conversation", "agent.BuildBinding error convo=%q elapsed=%s err=%v", convoID, time.Since(start).String(), "conversation not found")
		return nil, fmt.Errorf("conversation not found: %s", strings.TrimSpace(input.ConversationID))
	}
	ctx = applySchedulerDiscoveryMode(ctx, conv)

	// Compute effective preview limit using service defaults only
	histStart := time.Now()
	logx.Infof("conversation", "agent.BuildBinding buildHistory start convo=%q", convoID)
	histResult, err := s.buildHistoryWithLimit(ctx, conv.GetTranscript(), input)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding buildHistory error convo=%q elapsed=%s err=%v", convoID, time.Since(histStart).String(), err)
		return nil, err
	}
	logx.Infof("conversation", "agent.BuildBinding buildHistory ok convo=%q elapsed=%s overflow=%t elicitation=%d", convoID, time.Since(histStart).String(), histResult.Overflow, len(histResult.Elicitation))
	b.History = histResult.History
	// Align History.CurrentTurnID with the in-flight turn when available
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		b.History.CurrentTurnID = strings.TrimSpace(tm.TurnID)
	}
	// Attach current-turn elicitation messages to History.Current so
	// they can participate in a unified, chronological view of the
	// in-flight turn.
	if len(histResult.Elicitation) > 0 {
		appendCurrentMessages(&b.History, histResult.Elicitation...)
	}
	if extra := loopHistoryMessagesFromContext(ctx); len(extra) > 0 {
		appendCurrentMessages(&b.History, extra...)
	}
	// Populate History.LastResponse using the last assistant message in transcript
	if conv != nil {
		tr := conv.GetTranscript()
		if last := tr.LastAssistantMessageWithModelCall(); last != nil {
			trace := &binding.Trace{At: last.CreatedAt, Kind: binding.KindResponse}
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
	logx.Infof("conversation", "agent.BuildBinding buildToolSignatures start convo=%q", convoID)
	b.Tools.Signatures, err = s.buildToolSignatures(ctx, input)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding buildToolSignatures error convo=%q elapsed=%s err=%v", convoID, time.Since(toolsStart).String(), err)
		return nil, err
	}
	logx.Infof("conversation", "agent.BuildBinding buildToolSignatures ok convo=%q elapsed=%s tools=%d", convoID, time.Since(toolsStart).String(), len(b.Tools.Signatures))

	// Tool executions exposure: default "turn"; allow QueryInput override; then agent setting.
	exposure := resolveToolCallExposure(input)

	b.History.ToolExposure = string(exposure)

	execStart := time.Now()
	logx.Infof("conversation", "agent.BuildBinding buildToolExecutions start convo=%q exposure=%q", convoID, string(exposure))
	execResult, err := s.buildToolExecutions(ctx, input, conv, exposure)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding buildToolExecutions error convo=%q elapsed=%s err=%v", convoID, time.Since(execStart).String(), err)
		return nil, err
	}
	logx.Infof("conversation", "agent.BuildBinding buildToolExecutions ok convo=%q elapsed=%s overflow=%t", convoID, time.Since(execStart).String(), execResult.Overflow)

	// Drive overflow-based helper exposure via binding flag
	if execResult.Overflow {
		b.Flags.HasMessageOverflow = true
	}
	if execResult.MaxOverflowBytes > 0 && execResult.MaxOverflowBytes > b.Flags.MaxOverflowBytes {
		b.Flags.MaxOverflowBytes = execResult.MaxOverflowBytes
	}

	// If any tool call in the current turn overflowed, expose callToolResult tools
	turnMeta, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if ok && strings.TrimSpace(turnMeta.TurnID) != "" {
		var current *apiconv.Turn
		for _, t := range conv.GetTranscript() {
			if t != nil && t.Id == turnMeta.TurnID {
				current = t
				break
			}
		}
		s.handleOverflow(ctx, input, current, b)
		s.ensureInternalToolsIfNeeded(ctx, input, b)
		// Allow tool-use if we appended any
		if len(b.Tools.Signatures) > 0 && b.Model != "" {
			b.Flags.CanUseTool = s.llm.ModelImplements(ctx, b.Model, base.CanUseTools)
		}

	}

	docsStart := time.Now()
	logx.Infof("conversation", "agent.BuildBinding buildDocuments start convo=%q", convoID)
	docs, err := s.buildDocumentsBinding(ctx, input, false)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding buildDocuments error convo=%q elapsed=%s err=%v", convoID, time.Since(docsStart).String(), err)
		return nil, err
	}

	logx.Infof("conversation", "agent.BuildBinding buildDocuments ok convo=%q elapsed=%s docs=%d", convoID, time.Since(docsStart).String(), len(docs.Items))
	b.Documents = docs
	// Normalize user doc URIs by trimming workspace root for stable display
	s.normalizeDocURIs(&b.Documents, workspace.Root())
	// Attach non-text user documents as binary attachments (e.g., PDFs, images)
	s.attachNonTextUserDocs(ctx, b)

	sysDocsStart := time.Now()
	logx.Infof("conversation", "agent.BuildBinding buildSystemDocuments start convo=%q", convoID)
	b.SystemDocuments, err = s.buildDocumentsBinding(ctx, input, true)
	if err != nil {
		logx.Infof("conversation", "agent.BuildBinding buildSystemDocuments error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	logx.Infof("conversation", "agent.BuildBinding buildSystemDocuments ok convo=%q elapsed=%s docs=%d", convoID, time.Since(sysDocsStart).String(), len(b.SystemDocuments.Items))
	s.appendTranscriptSystemDocs(conv.GetTranscript(), b)
	if err := s.applySelectedTemplate(ctx, input, b); err != nil {
		logx.Infof("conversation", "agent.BuildBinding applySelectedTemplate error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	if err := s.applySelectedPromptProfile(ctx, input, b); err != nil {
		logx.Infof("conversation", "agent.BuildBinding applySelectedPromptProfile error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	if err := s.appendToolPlaybooks(ctx, b.Tools.Signatures, &b.SystemDocuments); err != nil {
		logx.Infof("conversation", "agent.BuildBinding appendToolPlaybooks error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	if err := s.appendBootstrapSystemDocuments(ctx, input, b); err != nil {
		logx.Infof("conversation", "agent.BuildBinding appendBootstrapSystemDocuments error convo=%q elapsed=%s err=%v", convoID, time.Since(sysDocsStart).String(), err)
		return nil, err
	}
	s.appendAgentDirectoryDoc(ctx, input, &b.SystemDocuments)
	b.Tools.Signatures = filterDelegationDiscoveryTools(b.Tools.Signatures, &b.SystemDocuments)
	// Normalize system doc URIs similarly (even if not rendered now)
	s.normalizeDocURIs(&b.SystemDocuments, workspace.Root())
	if s.skillSvc != nil {
		b.Skills, b.SkillsPrompt = s.skillSvc.Visible(input.Agent)
	}

	if name, body, ok := runtimeActivatedSkill(input); ok && !runtimeActivatedSkillEmbedded(input) {
		uri := "internal://active-skill/" + strings.TrimSpace(name)
		if !hasDocumentURI(b.SystemDocuments.Items, uri) {
			b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
				Title:       "Active Skill: " + strings.TrimSpace(name),
				PageContent: strings.TrimSpace(body),
				SourceURI:   uri,
				MimeType:    "text/markdown",
				Metadata: map[string]string{
					"kind":  "active_skill",
					"skill": strings.TrimSpace(name),
				},
			})
		}
		b.SkillsPrompt = ""
	}
	b.Context = input.Context

	// Expose tool availability flags for templates (dynamic tool selection).
	// Avoid mutating input.Context directly by working on a copy.
	b.Context = cloneContextMap(b.Context)
	mergeElicitationPayloadIntoContext(b.History, &b.Context)
	applyProjectionContext(ctx, &b.Context)
	s.applyWorkdirContext(input, b)
	s.applyDelegationContext(input, b)

	logx.Infof("conversation", "agent.BuildBinding ok convo=%q elapsed=%s history_msgs=%d sys_docs=%d docs=%d tools=%d", convoID, time.Since(start).String(), len(b.History.Messages), len(b.SystemDocuments.Items), len(b.Documents.Items), len(b.Tools.Signatures))
	if debugtrace.Enabled() {
		debugtrace.Write("agent", "build_binding", map[string]any{
			"conversationID": convoID,
			"elapsedMs":      time.Since(start).Milliseconds(),
			"model":          strings.TrimSpace(b.Model),
			"currentTurnID":  strings.TrimSpace(b.History.CurrentTurnID),
			"toolExposure":   strings.TrimSpace(b.History.ToolExposure),
			"lastResponse": map[string]any{
				"id":   bindingTraceID(b.History.LastResponse),
				"kind": bindingTraceKind(b.History.LastResponse),
			},
			"traceCount":      len(b.History.Traces),
			"historyMessages": debugtrace.SummarizeMessages(b.History.LLMMessages()),
			"toolNames":       bindingToolNames(b.Tools.Signatures),
			"contextJSON":     b.ContextJSON(),
		})
	}
	return b, nil
}

func (s *Service) applySelectedTemplate(ctx context.Context, input *QueryInput, b *binding.Binding) error {
	if s == nil || input == nil || b == nil || s.templateRepo == nil {
		return nil
	}
	templateID := strings.TrimSpace(input.TemplateId)
	if templateID == "" {
		return nil
	}
	tpl, err := s.templateRepo.Load(ctx, templateID)
	if err != nil {
		return fmt.Errorf("load template %q: %w", templateID, err)
	}
	if tpl == nil {
		return fmt.Errorf("template %q not found", templateID)
	}
	content := strings.TrimSpace(templatesvc.RenderTemplateDocument(tpl))
	if content == "" {
		return nil
	}
	uri := "template://" + strings.TrimSpace(tpl.Name)
	if !hasDocumentURI(b.SystemDocuments.Items, uri) {
		b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
			Title:       strings.TrimSpace(tpl.Name),
			PageContent: content,
			SourceURI:   uri,
			MimeType:    "text/markdown",
			Metadata:    map[string]string{"kind": "template", "template": strings.TrimSpace(tpl.Name)},
		})
	}
	b.Tools.Signatures = filterToolSignaturesByServicePrefix(b.Tools.Signatures, "template-")
	return nil
}

func (s *Service) bindingConversation(ctx context.Context, input *QueryInput) (*apiconv.Conversation, error) {
	return s.fetchConversationWithRetry(ctx, input.ConversationID, apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true), apiconv.WithIncludeModelCall(true))
}

func resolveToolCallExposure(input *QueryInput) agent.ToolCallExposure {
	exposure := agent.ToolCallExposure("turn")
	if input == nil {
		return exposure
	}
	if input.ToolCallExposure != nil && strings.TrimSpace(string(*input.ToolCallExposure)) != "" {
		return *input.ToolCallExposure
	}
	if input.Agent != nil && strings.TrimSpace(string(input.Agent.Tool.CallExposure)) != "" {
		return input.Agent.Tool.CallExposure
	}
	if input.Agent != nil && strings.TrimSpace(string(input.Agent.ToolCallExposure)) != "" {
		return input.Agent.ToolCallExposure
	}
	return exposure
}

func bindingTraceID(trace *binding.Trace) string {
	if trace == nil {
		return ""
	}
	return strings.TrimSpace(trace.ID)
}

func bindingTraceKind(trace *binding.Trace) string {
	if trace == nil {
		return ""
	}
	return strings.TrimSpace(string(trace.Kind))
}

func bindingToolNames(defs []*llm.ToolDefinition) []string {
	if len(defs) == 0 {
		return nil
	}
	result := make([]string, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		if name := strings.TrimSpace(def.Name); name != "" {
			result = append(result, name)
		}
	}
	return result
}

type loopHistoryContextKey struct{}

func withLoopHistoryMessages(ctx context.Context, msgs []*binding.Message) context.Context {
	if len(msgs) == 0 {
		return ctx
	}
	cloned := make([]*binding.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		copyMsg := *msg
		if len(msg.Attachment) > 0 {
			copyMsg.Attachment = append([]*binding.Attachment(nil), msg.Attachment...)
		}
		if len(msg.ToolArgs) > 0 {
			args := make(map[string]interface{}, len(msg.ToolArgs))
			for k, v := range msg.ToolArgs {
				args[k] = v
			}
			copyMsg.ToolArgs = args
		}
		cloned = append(cloned, &copyMsg)
	}
	if len(cloned) == 0 {
		return ctx
	}
	return context.WithValue(ctx, loopHistoryContextKey{}, cloned)
}

func loopHistoryMessagesFromContext(ctx context.Context) []*binding.Message {
	if ctx == nil {
		return nil
	}
	msgs, _ := ctx.Value(loopHistoryContextKey{}).([]*binding.Message)
	return msgs
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

func applyProjectionContext(ctx context.Context, target *map[string]interface{}) {
	if target == nil {
		return
	}
	if *target == nil {
		*target = map[string]interface{}{}
	}
	snapshot, ok := runtimeprojection.SnapshotFromContext(ctx)
	if !ok {
		return
	}
	(*target)["Projection"] = map[string]interface{}{
		"scope":              strings.TrimSpace(snapshot.Scope),
		"reason":             strings.TrimSpace(snapshot.Reason),
		"hiddenTurnCount":    len(snapshot.HiddenTurnIDs),
		"hiddenMessageCount": len(snapshot.HiddenMessageIDs),
	}
}

func (s *Service) applyDelegationContext(input *QueryInput, b *binding.Binding) {
	if input == nil || b == nil || input.Agent == nil {
		logx.Infof("conversation", "delegation.context skip missing input/binding/agent")
		return
	}
	if input.Agent.Delegation == nil || !input.Agent.Delegation.Enabled {
		logx.Infof("conversation", "delegation.context disabled agent_id=%q", strings.TrimSpace(input.Agent.ID))
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
	currentDepth := delegationDepthFromContextMap(b.Context, strings.TrimSpace(input.Agent.ID))
	b.Context["DelegationCurrentDepth"] = currentDepth
	b.Context["DelegationIsDelegated"] = currentDepth > 0
	remainingDepth := maxDepth - currentDepth
	if remainingDepth < 0 {
		remainingDepth = 0
	}
	b.Context["DelegationRemainingDepth"] = remainingDepth
	logx.Infof("conversation", "delegation.context enabled agent_id=%q maxDepth=%d", strings.TrimSpace(input.Agent.ID), maxDepth)
}

func (s *Service) applyWorkdirContext(input *QueryInput, b *binding.Binding) {
	if input == nil || b == nil || b.Context == nil {
		return
	}
	if input.Agent != nil {
		if value := strings.TrimSpace(input.Agent.DefaultWorkdir); value != "" {
			b.Context["AgentDefaultWorkdir"] = value
		}
	}
	if value := normalizeWorkdirValue(b.Context["workdir"]); value != "" {
		b.Context["workdir"] = value
		b.Context["ResolvedWorkdir"] = value
		return
	}
	if value := normalizeWorkdirValue(b.Context["resolvedWorkdir"]); value != "" {
		b.Context["resolvedWorkdir"] = value
		b.Context["ResolvedWorkdir"] = value
	}
}

func delegationDepthFromContextMap(ctx map[string]interface{}, agentID string) int {
	if len(ctx) == 0 || strings.TrimSpace(agentID) == "" {
		return 0
	}
	raw, ok := ctx["DelegationDepths"]
	if !ok || raw == nil {
		return 0
	}
	m, ok := raw.(map[string]interface{})
	if !ok {
		return 0
	}
	value, ok := m[agentID]
	if !ok || value == nil {
		return 0
	}
	switch actual := value.(type) {
	case int:
		if actual < 0 {
			return 0
		}
		return actual
	case int32:
		if actual < 0 {
			return 0
		}
		return int(actual)
	case int64:
		if actual < 0 {
			return 0
		}
		return int(actual)
	case float32:
		if actual < 0 {
			return 0
		}
		return int(actual)
	case float64:
		if actual < 0 {
			return 0
		}
		return int(actual)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(actual)); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}
