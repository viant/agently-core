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
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	"github.com/viant/agently-core/service/agent/prompts"
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
	out, pctx, err := s.runPlannerPass(ctx, input, tc)
	if err != nil {
		return err
	}
	s.applyPlannerOutput(input, out, pctx)
	payloadID, err := s.persistPlannerOutputPayload(ctx, out)
	if err != nil {
		return err
	}
	s.publishPlannerEvent(ctx, &streaming.Event{
		Type:                   streaming.EventTypePlannerOutput,
		ConversationID:         turn.ConversationID,
		TurnID:                 turn.TurnID,
		PlannerStrategyFamily:  out.StrategyFamily,
		PlannerAttempt:         pctx.Attempt,
		PlannerOutputPayloadID: payloadID,
		CreatedAt:              time.Now(),
	})
	return s.persistPlannerGuidance(ctx, &turn, input, out, pctx, payloadID)
}

func (s *Service) runPlannerPass(ctx context.Context, input *QueryInput, tc *intakesvc.TurnContext) (*planner.Output, *planner.PlannerContext, error) {
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

	b, err := s.BuildBinding(ctx, plannerInput)
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

	runCtx := s.ensureRunTrackedLLMContext(ctx, strings.TrimSpace(input.ConversationID), "planner_pass", strings.TrimSpace(input.MessageID))
	runCtx = runtimerequestctx.WithRequestMode(runCtx, "plan")
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
	modelSelection.Options.OutputSchema = planner.JSONSchema()
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
				llm.NewSystemMessage(prompts.PlannerModePromptWithFeedback(formatPlannerValidationErrors(prevErrs), scenarioCatalog)),
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
		genInput.ModelSelection.Options.OutputSchema = planner.JSONSchema()

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
		parsed, errs, err := planner.New().Run(raw, s.plannerValidationContext(input))
		if err != nil {
			return nil, nil, err
		}
		if len(errs) == 0 {
			s.writePlannerPassTrace(input, attempt, true, parsed, nil)
			validated := true
			s.publishPlannerEvent(runCtx, &streaming.Event{
				Type:                  streaming.EventTypePlannerValidated,
				PlannerAttempt:        attempt,
				ConversationID:        strings.TrimSpace(input.ConversationID),
				TurnID:                strings.TrimSpace(input.MessageID),
				PlannerValidated:      &validated,
				PlannerStrategyFamily: parsed.StrategyFamily,
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
	if hasDocumentURI(b.SystemDocuments.Items, sourceURI) {
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

func (s *Service) resolvePlannerExecutionInput(ctx context.Context, input *QueryInput, tc *intakesvc.TurnContext) (*agentmdl.Agent, *QueryInput, error) {
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

func (s *Service) persistPlannerGuidance(ctx context.Context, turn *runtimerequestctx.TurnMeta, input *QueryInput, out *planner.Output, pctx *planner.PlannerContext, payloadID string) error {
	if s == nil || s.conversation == nil {
		return fmt.Errorf("planner guidance: conversation client not configured")
	}
	if turn == nil {
		return fmt.Errorf("planner guidance: turn is required")
	}
	if out == nil {
		return fmt.Errorf("planner guidance: output is required")
	}
	type docDef struct {
		id      string
		summary string
		content string
	}
	docs := []docDef{
		{
			id:      "planner-strategy:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://strategy",
			content: renderPlannerStrategy(out, pctx, plannerDocMetadata{
				Status:          "validated",
				StaticProfile:   strings.TrimSpace(input.PromptProfileId),
				OutputPayloadID: strings.TrimSpace(payloadID),
				Validated:       llm.BoolPtr(true),
			}),
		},
		{
			id:      "planner-evidence:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://evidence",
			content: renderPlannerEvidence(out),
		},
		{
			id:      "planner-guards:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://guards",
			content: renderPlannerGuards(out),
		},
		{
			id:      "planner-policy:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://policy",
			content: renderPlannerPolicy(out, plannerDocMetadata{
				Validated: llm.BoolPtr(true),
			}, nil),
		},
	}
	for _, doc := range docs {
		content := strings.TrimSpace(doc.content)
		if content == "" {
			continue
		}
		if _, err := apiconv.AddMessage(ctx, s.conversation, turn,
			apiconv.WithId(doc.id),
			apiconv.WithRole("system"),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("planner"),
			apiconv.WithMode(toolexec.SystemDocumentMode),
			apiconv.WithTags(toolexec.SystemDocumentTag),
			apiconv.WithContextSummary(doc.summary),
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
	docs := []struct {
		id      string
		summary string
		content string
	}{
		{
			id:      "planner-strategy:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://strategy",
			content: renderPlannerStrategy(nil, &planner.PlannerContext{
				Trigger: planner.Trigger(plannerTriggerFromInput(input)),
				Attempt: 2,
			}, plannerDocMetadata{
				Status:        "failed",
				StaticProfile: strings.TrimSpace(input.PromptProfileId),
				Validated:     &validated,
			}),
		},
		{
			id:      "planner-policy:" + strings.TrimSpace(turn.TurnID),
			summary: "planner://policy",
			content: renderPlannerPolicy(nil, plannerDocMetadata{
				SecondPolicy: strings.TrimSpace(policy),
				Validated:    &validated,
			}, errs),
		},
	}
	for _, doc := range docs {
		if strings.TrimSpace(doc.content) == "" {
			continue
		}
		if _, err := apiconv.AddMessage(ctx, s.conversation, &turn,
			apiconv.WithId(doc.id),
			apiconv.WithRole("system"),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("planner"),
			apiconv.WithMode(toolexec.SystemDocumentMode),
			apiconv.WithTags(toolexec.SystemDocumentTag),
			apiconv.WithContextSummary(doc.summary),
			apiconv.WithContent(strings.TrimSpace(doc.content)),
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
	if s == nil || s.promptRepo == nil || input == nil || input.Agent == nil {
		return ""
	}
	profiles, err := s.promptRepo.LoadAll(ctx)
	if err != nil {
		return ""
	}
	return plannerscenarios.Catalog(profiles, input.Agent.Prompts.Bundles)
}

func formatPlannerValidationErrors(errs []planner.ValidationError) string {
	if len(errs) == 0 {
		return ""
	}
	lines := make([]string, 0, len(errs))
	for _, err := range errs {
		text := strings.TrimSpace(err.Message)
		if text == "" {
			text = err.Error()
		}
		if text == "" {
			continue
		}
		lines = append(lines, "- "+text)
	}
	return strings.Join(lines, "\n")
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
	} else if text := formatPlannerValidationErrors(errs); text != "" {
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

func (s *Service) persistPlannerOutputPayload(ctx context.Context, out *planner.Output) (string, error) {
	if s == nil || s.conversation == nil || out == nil {
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

func (s *Service) writePlannerPassTrace(input *QueryInput, attempt int, validated bool, out *planner.Output, errs []planner.ValidationError) {
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
	if out != nil {
		trace.StrategyFamily = out.StrategyFamily
		trace.BaseProfiles = append([]string(nil), out.BaseProfiles...)
		trace.ToolBundles = append([]string(nil), out.ToolBundles...)
		trace.TemplateID = out.TemplateID
		trace.EvidenceCount = len(out.RequiredEvidence)
		trace.ExecutionOrder = append([]string(nil), out.ExecutionOrder...)
		trace.Guards = append([]string(nil), out.FinalizationGuards...)
	}
	debugtrace.Write("agent", "planner_pass", trace.AsMap())
}

func (s *Service) applyPlannerOutput(input *QueryInput, out *planner.Output, pctx *planner.PlannerContext) {
	if input == nil || out == nil {
		return
	}
	if len(out.ToolBundles) > 0 {
		input.ToolBundles = normalizeStringList(append(input.ToolBundles, out.ToolBundles...))
	}
	if id := strings.TrimSpace(out.TemplateID); id != "" && strings.TrimSpace(input.TemplateId) == "" {
		input.TemplateId = id
	}
	if out.ParallelToolCalls != nil && input.ParallelToolCalls == nil {
		v := *out.ParallelToolCalls
		input.ParallelToolCalls = &v
	}
	if pctx != nil {
		if input.Context == nil {
			input.Context = make(map[string]any)
		}
		input.Context[planner.ContextKey] = pctx
	}
}

type plannerDocMetadata struct {
	Status          string
	StaticProfile   string
	SecondPolicy    string
	OutputPayloadID string
	Validated       *bool
}

func renderPlannerStrategy(out *planner.Output, pctx *planner.PlannerContext, meta plannerDocMetadata) string {
	var parts []string
	if value := strings.TrimSpace(meta.Status); value != "" {
		parts = append(parts, "Status: "+value)
	}
	if pctx != nil {
		if value := strings.TrimSpace(string(pctx.Trigger)); value != "" {
			parts = append(parts, "Trigger: "+value)
		}
		if pctx.Attempt > 0 {
			parts = append(parts, fmt.Sprintf("Attempt: %d", pctx.Attempt))
		}
	}
	if value := strings.TrimSpace(meta.StaticProfile); value != "" {
		parts = append(parts, "StaticProfile: "+value)
	}
	if meta.Validated != nil {
		parts = append(parts, fmt.Sprintf("Validated: %t", *meta.Validated))
	}
	if value := strings.TrimSpace(meta.OutputPayloadID); value != "" {
		parts = append(parts, "OutputPayloadID: "+value)
	}
	if out == nil {
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if value := strings.TrimSpace(out.StrategyFamily); value != "" {
		parts = append(parts, "StrategyFamily: "+value)
	}
	if len(out.BaseProfiles) > 0 {
		parts = append(parts, "BaseProfiles: "+strings.Join(out.BaseProfiles, ", "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func renderPlannerEvidence(out *planner.Output) string {
	if out == nil {
		return ""
	}
	var parts []string
	if len(out.RequiredEvidence) > 0 {
		parts = append(parts, "RequiredEvidence: "+strings.Join(out.RequiredEvidence, ", "))
	}
	if len(out.ExecutionOrder) > 0 {
		parts = append(parts, "ExecutionOrder: "+strings.Join(out.ExecutionOrder, ", "))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func renderPlannerGuards(out *planner.Output) string {
	if out == nil || len(out.FinalizationGuards) == 0 {
		return ""
	}
	return "FinalizationGuards: " + strings.Join(out.FinalizationGuards, ", ")
}

func renderPlannerPolicy(out *planner.Output, meta plannerDocMetadata, errs []planner.ValidationError) string {
	var parts []string
	if value := strings.TrimSpace(meta.SecondPolicy); value != "" {
		parts = append(parts, "SecondPolicy: "+value)
	}
	if meta.Validated != nil {
		parts = append(parts, fmt.Sprintf("Validated: %t", *meta.Validated))
	}
	if out != nil {
		if len(out.NarrationPolicy) > 0 {
			if raw, err := json.MarshalIndent(out.NarrationPolicy, "", "  "); err == nil {
				parts = append(parts, "NarrationPolicy:\n```json\n"+string(raw)+"\n```")
			}
		}
		if len(out.WorkspaceExtensions) > 0 {
			if raw, err := json.MarshalIndent(out.WorkspaceExtensions, "", "  "); err == nil {
				parts = append(parts, "WorkspaceExtensions:\n```json\n"+string(raw)+"\n```")
			}
		}
	}
	if text := formatPlannerValidationErrors(errs); text != "" {
		parts = append(parts, "ValidationErrors:\n"+text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
