package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/debugtrace"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/agent/prompts"
	agenttool "github.com/viant/agently-core/service/agent/tool"
	"github.com/viant/agently-core/service/core"
	intakesvc "github.com/viant/agently-core/service/intake"
	planner "github.com/viant/agently-core/service/planner"
	plannerscenarios "github.com/viant/agently-core/service/planner/scenarios"
	toolexec "github.com/viant/agently-core/service/shared/toolexec"
)

type plannerHandledError struct {
	content string
	status  string
}

func (e *plannerHandledError) Error() string {
	if e == nil {
		return ""
	}
	return strings.TrimSpace(e.status)
}

func (s *Service) maybeRunPlannerPass(ctx context.Context, input *QueryInput) error {
	tc := intakesvc.FromContext(input.Context)
	if tc == nil || tc.Routing.Mode != intakesvc.ModePlanner {
		return nil
	}
	logx.Infof("conversation", "planner.pass.selected convo=%q agent=%q selectedAgent=%q trigger=%q plannerAgent=%q",
		strings.TrimSpace(input.ConversationID),
		strings.TrimSpace(input.AgentID),
		strings.TrimSpace(tc.Routing.SelectedAgentID),
		strings.TrimSpace(tc.Planner.Trigger),
		strings.TrimSpace(tc.Planner.AgentID),
	)
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.TurnID) == "" || strings.TrimSpace(turn.ConversationID) == "" {
		return fmt.Errorf("planner pass: turn metadata is required")
	}
	s.publishPlannerEvent(ctx, &streaming.Event{
		Type:                 streaming.EventTypePlannerSelected,
		ConversationID:       turn.ConversationID,
		TurnID:               turn.TurnID,
		PlannerTrigger:       strings.TrimSpace(tc.Planner.Trigger),
		PlannerStaticProfile: strings.TrimSpace(input.PromptProfileId),
		CreatedAt:            time.Now(),
	})
	plannerAgent, _, err := s.resolvePlannerExecutionInput(ctx, input, tc)
	if err != nil {
		return err
	}
	contract, err := s.resolvePlannerContract(ctx, plannerAgent)
	if err != nil {
		return err
	}
	out, pctx, err := s.runPlannerPass(ctx, input, tc, contract)
	if err != nil {
		return err
	}
	s.applyPlannerOutput(input, contract, out, pctx)
	payloadID, err := s.persistPlannerOutputPayload(ctx, out)
	if err != nil {
		return err
	}
	s.publishPlannerEvent(ctx, &streaming.Event{
		Type:                   streaming.EventTypePlannerOutput,
		ConversationID:         turn.ConversationID,
		TurnID:                 turn.TurnID,
		PlannerStrategyFamily:  planner.OutputString(out, "strategyFamily"),
		PlannerAttempt:         pctx.Attempt,
		PlannerOutputPayloadID: payloadID,
		CreatedAt:              time.Now(),
	})
	return s.persistPlannerGuidance(ctx, &turn, input, contract, out, pctx, payloadID)
}

func (s *Service) runPlannerPass(ctx context.Context, input *QueryInput, tc *intakesvc.Context, contract planner.Contract) (planner.Output, *planner.PlannerContext, error) {
	if s == nil || s.llm == nil {
		return nil, nil, fmt.Errorf("planner pass: llm service not configured")
	}
	if input == nil || input.Agent == nil {
		return nil, nil, fmt.Errorf("planner pass: agent input is required")
	}
	if tc == nil || strings.TrimSpace(tc.Routing.Mode) != intakesvc.ModePlanner {
		return nil, nil, fmt.Errorf("planner pass: planner mode turn context is required")
	}

	plannerAgent, plannerInput, err := s.resolvePlannerExecutionInput(ctx, input, tc)
	if err != nil {
		return nil, nil, err
	}
	if contract == nil {
		return nil, nil, fmt.Errorf("planner pass: planner contract is required")
	}
	schema, err := contract.Schema(ctx)
	if err != nil {
		return nil, nil, err
	}

	runCtx := s.ensureRunTrackedLLMContext(ctx, strings.TrimSpace(input.ConversationID), "planner_pass", strings.TrimSpace(input.MessageID))
	runCtx = runtimerequestctx.WithRequestMode(runCtx, "plan")
	if tm, ok := runtimerequestctx.TurnMetaFromContext(runCtx); ok {
		tm.Assistant = strings.TrimSpace(plannerAgent.ID)
		runCtx = runtimerequestctx.WithTurnMeta(runCtx, tm)
	}

	b, err := s.BuildBinding(runCtx, plannerInput)
	if err != nil {
		return nil, nil, err
	}
	if b == nil {
		b = &binding.Binding{}
	}
	// Hard guarantee: planner pass has no callable tools regardless of what the
	// normal binding would have exposed on the execution pass.
	b.Tools.Signatures = nil
	b.Flags.CanUseTool = false

	if err := s.appendPlannerControlDocs(runCtx, input, plannerInput, plannerAgent, b); err != nil {
		return nil, nil, err
	}

	modelSelection := plannerAgent.ModelSelection
	if strings.TrimSpace(input.ModelOverride) != "" {
		modelSelection.Model = strings.TrimSpace(input.ModelOverride)
	}
	baseOptions := &llm.Options{}
	if modelSelection.Options != nil {
		copied := *modelSelection.Options
		baseOptions = &copied
	}
	modelSelection.Options = baseOptions
	modelSelection.Options.ToolChoice = llm.NewNoneToolChoice()
	modelSelection.Options.Tools = nil
	modelSelection.Options.Mode = "plan"
	modelSelection.Options.JSONMode = true
	modelSelection.Options.ResponseMIMEType = "application/json"
	modelSelection.Options.OutputSchema = schema
	if modelSelection.Options.Metadata == nil {
		modelSelection.Options.Metadata = map[string]interface{}{}
	}
	modelSelection.Options.Metadata["modelSource"] = "planner.pass"
	if modelSelection.Options.Temperature == 0 {
		modelSelection.Options.Temperature = 0.7
	}
	scenarioCatalog := s.plannerScenarioCatalog(runCtx, input)

	var prevErrs []planner.ValidationError
	for attempt := 1; attempt <= 2; attempt++ {
		genInput := &core.GenerateInput{
			Message: []llm.Message{
				llm.NewSystemMessage(prompts.PlannerModePromptWithFeedback(planner.FormatValidationErrors(prevErrs), scenarioCatalog)),
			},
			Prompt:         plannerAgent.Prompt,
			SystemPrompt:   plannerAgent.SystemPrompt,
			Instruction:    plannerAgent.EffectiveInstructionPrompt(),
			Binding:        b,
			ModelSelection: modelSelection,
			UserID:         strings.TrimSpace(input.UserId),
			AgentID:        strings.TrimSpace(plannerAgent.ID),
		}
		EnsureGenerateOptions(runCtx, genInput, plannerAgent)
		genInput.ModelSelection.Options.ToolChoice = llm.NewNoneToolChoice()
		genInput.ModelSelection.Options.Tools = nil
		genInput.ModelSelection.Options.Mode = "plan"
		genInput.ModelSelection.Options.JSONMode = true
		genInput.ModelSelection.Options.ResponseMIMEType = "application/json"
		genInput.ModelSelection.Options.OutputSchema = schema

		out := &core.GenerateOutput{}
		if err := s.llm.Generate(runCtx, genInput, out); err != nil {
			return nil, nil, err
		}
		raw := strings.TrimSpace(out.Content)
		if raw == "" && out.Response != nil && len(out.Response.Choices) > 0 {
			raw = strings.TrimSpace(out.Response.Choices[0].Message.Content)
		}
		if raw == "" {
			return nil, nil, fmt.Errorf("planner pass: empty guidance")
		}
		parsed, err := contract.Parse(raw)
		if err != nil {
			return nil, nil, err
		}
		errs := contract.Validate(runCtx, parsed, s.plannerValidationContext(input))
		if len(errs) == 0 {
			s.writePlannerPassTrace(input, attempt, true, parsed, nil)
			validated := true
			s.publishPlannerEvent(runCtx, &streaming.Event{
				Type:                  streaming.EventTypePlannerValidated,
				PlannerAttempt:        attempt,
				ConversationID:        strings.TrimSpace(input.ConversationID),
				TurnID:                strings.TrimSpace(input.MessageID),
				PlannerValidated:      &validated,
				PlannerStrategyFamily: planner.OutputString(parsed, "strategyFamily"),
				CreatedAt:             time.Now(),
			})
			return parsed, planner.NewContext(planner.Trigger(strings.TrimSpace(tc.Planner.Trigger)), attempt, parsed), nil
		}
		s.writePlannerPassTrace(input, attempt, false, parsed, errs)
		validated := false
		s.publishPlannerEvent(runCtx, &streaming.Event{
			Type:             streaming.EventTypePlannerValidated,
			PlannerAttempt:   attempt,
			ConversationID:   strings.TrimSpace(input.ConversationID),
			TurnID:           strings.TrimSpace(input.MessageID),
			PlannerValidated: &validated,
			CreatedAt:        time.Now(),
		})
		prevErrs = errs
	}
	return nil, nil, s.handlePlannerSecondFailure(ctx, input, prevErrs)
}

func (s *Service) appendPlannerControlDocs(ctx context.Context, executionInput, plannerInput *QueryInput, plannerAgent *agentmdl.Agent, b *binding.Binding) error {
	if s == nil || b == nil {
		return nil
	}
	if s.registry == nil {
		return fmt.Errorf("planner pass: tool registry not configured")
	}
	if err := s.appendPlannerControlDoc(ctx, b, "planner/agent-topology", "internal://planner/agent-topology", "llm/agents:topology", map[string]interface{}{}); err != nil {
		return err
	}
	tools, err := s.resolveTools(ctx, executionInput)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Definition.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 && plannerAgent != nil && plannerInput != nil {
		tools, err = s.resolveTools(ctx, plannerInput)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			name := strings.TrimSpace(tool.Definition.Name)
			if name == "" {
				continue
			}
			key := strings.ToLower(name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return s.appendPlannerControlDoc(ctx, b, "planner/tool-details", "internal://planner/tool-details", "llm/agents:tool_details", map[string]interface{}{"names": names})
}

func (s *Service) appendPlannerControlDoc(ctx context.Context, b *binding.Binding, title, sourceURI, toolName string, args map[string]interface{}) error {
	if b == nil || s == nil || s.registry == nil {
		return nil
	}
	if hasDocumentURI(b.SystemDocuments.Items, sourceURI) || hasDocumentTool(b.SystemDocuments.Items, toolName) {
		return nil
	}
	result, err := s.registry.Execute(ctx, toolName, args)
	if err != nil {
		return fmt.Errorf("planner pass: %s failed: %w", toolName, err)
	}
	payload := strings.TrimSpace(result)
	if payload == "" {
		return nil
	}
	argsJSON := "{}"
	if len(args) > 0 {
		if data, err := json.MarshalIndent(args, "", "  "); err == nil {
			argsJSON = string(data)
		}
	}
	var body strings.Builder
	body.WriteString("# Planner Control Tool Result\n\n")
	body.WriteString("- Tool: `")
	body.WriteString(strings.TrimSpace(toolName))
	body.WriteString("`\n")
	body.WriteString("- Args:\n\n```json\n")
	body.WriteString(argsJSON)
	body.WriteString("\n```\n\n")
	body.WriteString("## Result\n\n```json\n")
	body.WriteString(payload)
	body.WriteString("\n```")
	b.SystemDocuments.Items = append(b.SystemDocuments.Items, &binding.Document{
		Title:       title,
		PageContent: body.String(),
		SourceURI:   sourceURI,
		MimeType:    "text/markdown",
		Metadata: map[string]string{
			"kind": "planner_control",
			"tool": strings.TrimSpace(toolName),
		},
	})
	return nil
}

func hasDocumentTool(items []*binding.Document, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return false
	}
	for _, doc := range items {
		if doc == nil || doc.Metadata == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(doc.Metadata["tool"]), toolName) {
			return true
		}
	}
	return false
}

func (s *Service) resolvePlannerExecutionInput(ctx context.Context, input *QueryInput, tc *intakesvc.Context) (*agentmdl.Agent, *QueryInput, error) {
	if input == nil || input.Agent == nil {
		return nil, nil, fmt.Errorf("planner pass: execution agent is required")
	}
	plannerAgent := input.Agent
	plannerAgentID := strings.TrimSpace(tc.Planner.AgentID)
	if plannerAgentID == "" {
		plannerAgentID = strings.TrimSpace(input.Agent.Intake.PlannerAgentID)
	}
	if plannerAgentID != "" && !strings.EqualFold(plannerAgentID, strings.TrimSpace(input.Agent.ID)) {
		loaded, err := s.loadResolvedAgent(ctx, plannerAgentID)
		if err != nil {
			return nil, nil, fmt.Errorf("planner pass: failed to load planner agent %q: %w", plannerAgentID, err)
		}
		if loaded == nil {
			return nil, nil, fmt.Errorf("planner pass: planner agent %q not found", plannerAgentID)
		}
		plannerAgent = loaded
	}
	plannerInput := *input
	plannerInput.Agent = plannerAgent
	plannerInput.AgentID = strings.TrimSpace(plannerAgent.ID)
	return plannerAgent, &plannerInput, nil
}

func (s *Service) resolvePlannerContract(ctx context.Context, plannerAgent *agentmdl.Agent) (planner.Contract, error) {
	if s == nil || s.plannerContracts == nil {
		return nil, fmt.Errorf("planner pass: planner contract resolver not configured")
	}
	contract, err := s.plannerContracts.Resolve(ctx, plannerAgent)
	if err != nil {
		return nil, fmt.Errorf("planner pass: failed to resolve planner contract: %w", err)
	}
	if contract == nil {
		return nil, fmt.Errorf("planner pass: planner contract resolver returned nil contract")
	}
	return contract, nil
}

func (s *Service) persistPlannerGuidance(ctx context.Context, turn *runtimerequestctx.TurnMeta, input *QueryInput, contract planner.Contract, out planner.Output, pctx *planner.PlannerContext, payloadID string) error {
	if s == nil || s.conversation == nil {
		return fmt.Errorf("planner guidance: conversation client not configured")
	}
	if turn == nil {
		return fmt.Errorf("planner guidance: turn is required")
	}
	if len(out) == 0 {
		return fmt.Errorf("planner guidance: output is required")
	}
	if contract == nil {
		return fmt.Errorf("planner guidance: planner contract is required")
	}
	docs := contract.GuidanceDocs(strings.TrimSpace(turn.TurnID), out, pctx, planner.GuidanceMeta{
		Status:          "validated",
		StaticProfile:   strings.TrimSpace(input.PromptProfileId),
		OutputPayloadID: strings.TrimSpace(payloadID),
		Validated:       llm.BoolPtr(true),
	}, nil)
	for _, doc := range docs {
		content := strings.TrimSpace(doc.Content)
		if content == "" {
			continue
		}
		if _, err := apiconv.AddMessage(ctx, s.conversation, turn,
			apiconv.WithId(doc.ID),
			apiconv.WithRole("system"),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("planner"),
			apiconv.WithMode(toolexec.SystemDocumentMode),
			apiconv.WithTags(toolexec.SystemDocumentTag),
			apiconv.WithContextSummary(doc.Summary),
			apiconv.WithContent(content),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistPlannerFailureState(ctx context.Context, input *QueryInput, policy string, errs []planner.ValidationError) error {
	if s == nil || s.conversation == nil || input == nil {
		return nil
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.TurnID) == "" || strings.TrimSpace(turn.ConversationID) == "" {
		return nil
	}
	validated := false
	contract, err := s.resolvePlannerContract(ctx, input.Agent)
	if err != nil {
		return err
	}
	docs := contract.GuidanceDocs(strings.TrimSpace(turn.TurnID), nil, &planner.PlannerContext{
		Trigger: planner.Trigger(plannerTriggerFromInput(input)),
		Attempt: 2,
	}, planner.GuidanceMeta{
		Status:        "failed",
		StaticProfile: strings.TrimSpace(input.PromptProfileId),
		SecondPolicy:  strings.TrimSpace(policy),
		Validated:     &validated,
	}, errs)
	for _, doc := range docs {
		if strings.TrimSpace(doc.Content) == "" {
			continue
		}
		if _, err := apiconv.AddMessage(ctx, s.conversation, &turn,
			apiconv.WithId(doc.ID),
			apiconv.WithRole("system"),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("planner"),
			apiconv.WithMode(toolexec.SystemDocumentMode),
			apiconv.WithTags(toolexec.SystemDocumentTag),
			apiconv.WithContextSummary(doc.Summary),
			apiconv.WithContent(strings.TrimSpace(doc.Content)),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) plannerValidationContext(input *QueryInput) planner.ValidationContext {
	return planner.ValidationContext{
		ProfileRepo:        s.promptRepo,
		ToolBundleRepo:     s.toolBundleRepo,
		TemplateRepo:       s.templateRepo,
		TemplateBundleRepo: s.templateBundleRepo,
		Agent:              input.Agent,
	}
}

func (s *Service) plannerScenarioCatalog(ctx context.Context, input *QueryInput) string {
	if s == nil || input == nil || input.Agent == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if s.promptRepo != nil {
		profiles, err := s.promptRepo.LoadAll(ctx)
		if err == nil {
			if profileKnowledge := plannerscenarios.Catalog(profiles, input.Agent.Prompts.Bundles); strings.TrimSpace(profileKnowledge) != "" {
				parts = append(parts, profileKnowledge)
			}
		}
	}
	if s.skillSvc != nil {
		if skillKnowledge := plannerscenarios.SkillCatalog(s.skillSvc.VisibleSkillsByName(input.Agent, input.Agent.Skills)); strings.TrimSpace(skillKnowledge) != "" {
			parts = append(parts, skillKnowledge)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func (s *Service) handlePlannerSecondFailure(ctx context.Context, input *QueryInput, errs []planner.ValidationError) error {
	policy := "clarify"
	if input != nil && input.Agent != nil {
		if value := strings.TrimSpace(input.Agent.Intake.PlannerSecondFailurePolicy); value != "" {
			policy = strings.ToLower(value)
		}
	}
	content := "I need clarification before I can plan this turn."
	status := "planner.clarify"
	if policy == "block" {
		content = "I can't safely plan this turn."
		status = "planner.block"
	} else if text := planner.FormatValidationErrors(errs); text != "" {
		content += "\n\n" + text
	}
	if err := s.persistPlannerFailureState(ctx, input, policy, errs); err != nil {
		return err
	}
	if err := s.publishAssistantMessageWithStatus(ctx, input, content, status); err != nil {
		return err
	}
	validated := false
	s.publishPlannerEvent(ctx, &streaming.Event{
		Type:                streaming.EventTypePlannerFailed,
		ConversationID:      strings.TrimSpace(input.ConversationID),
		TurnID:              strings.TrimSpace(input.MessageID),
		PlannerTrigger:      plannerTriggerFromInput(input),
		PlannerAttempt:      2,
		PlannerSecondPolicy: policy,
		PlannerValidated:    &validated,
		CreatedAt:           time.Now(),
	})
	return &plannerHandledError{content: content, status: status}
}

func plannerTriggerFromInput(input *QueryInput) string {
	if input == nil {
		return ""
	}
	if tc := intakesvc.FromContext(input.Context); tc != nil {
		return strings.TrimSpace(tc.Planner.Trigger)
	}
	return ""
}

func (s *Service) persistPlannerOutputPayload(ctx context.Context, out planner.Output) (string, error) {
	if s == nil || s.conversation == nil || len(out) == 0 {
		return "", nil
	}
	data, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	turnID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
	}
	id := "planner-output:" + convID + ":" + turnID
	if strings.HasSuffix(id, "::") || strings.HasSuffix(id, ":") {
		id = "planner-output:" + turnID
	}
	payload := apiconv.NewPayload()
	payload.SetId(id)
	payload.SetKind("planner_output")
	payload.SetMimeType("application/json")
	payload.SetSizeBytes(len(data))
	payload.SetStorage("inline")
	payload.SetInlineBody(data)
	if err := s.conversation.PatchPayload(ctx, payload); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) publishPlannerEvent(ctx context.Context, ev *streaming.Event) {
	if s == nil || s.streamPub == nil || ev == nil {
		return
	}
	convID := strings.TrimSpace(ev.ConversationID)
	turnID := strings.TrimSpace(ev.TurnID)
	if convID == "" {
		convID = strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
		ev.ConversationID = convID
	}
	if turnID == "" {
		if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
			turnID = strings.TrimSpace(tm.TurnID)
			ev.TurnID = turnID
		}
	}
	if ev.StreamID == "" {
		ev.StreamID = convID
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now()
	}
	ev.NormalizeIdentity(convID, turnID)
	_ = s.streamPub.Publish(ctx, ev)
}

func (s *Service) writePlannerPassTrace(input *QueryInput, attempt int, validated bool, out planner.Output, errs []planner.ValidationError) {
	if !debugtrace.Enabled() || input == nil {
		return
	}
	trace := &PlannerPassTrace{
		ConversationID:  strings.TrimSpace(input.ConversationID),
		TurnID:          strings.TrimSpace(input.MessageID),
		Attempt:         attempt,
		Validated:       validated,
		ValidatorErrors: errs,
	}
	if len(out) != 0 {
		trace.StrategyFamily = planner.OutputString(out, "strategyFamily")
		trace.BaseProfiles = planner.OutputStringSlice(out, "baseProfiles")
		trace.ToolBundles = planner.OutputStringSlice(out, "toolBundles")
		trace.TemplateID = planner.OutputString(out, "templateId")
		trace.EvidenceCount = len(planner.OutputStringSlice(out, "requiredEvidence"))
		trace.ExecutionOrder = planner.OutputStringSlice(out, "executionOrder")
		trace.Guards = planner.OutputStringSlice(out, "finalizationGuards")
	}
	debugtrace.Write("agent", "planner_pass", trace.AsMap())
}

func (s *Service) applyPlannerOutput(input *QueryInput, contract planner.Contract, out planner.Output, pctx *planner.PlannerContext) {
	if input == nil || len(out) == 0 || contract == nil {
		return
	}
	app := &planner.Application{
		ToolBundles:       append([]string(nil), input.ToolBundles...),
		TemplateID:        strings.TrimSpace(input.TemplateId),
		ParallelToolCalls: input.ParallelToolCalls,
		Context:           input.Context,
	}
	contract.Apply(app, out, pctx)
	input.ToolBundles = agenttool.NormalizeBundleNames(app.ToolBundles)
	input.TemplateId = strings.TrimSpace(app.TemplateID)
	input.ParallelToolCalls = app.ParallelToolCalls
	input.Context = app.Context
}
