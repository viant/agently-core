package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	tpldef "github.com/viant/agently-core/protocol/template"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
)

// Service runs the intake sidecar LLM call and returns an intake Context.
type Service struct {
	llm          *core.Service
	profileRepo  *promptrepo.Repository
	templateRepo *tplrepo.Repository
	bundleRepo   *toolbundlerepo.Repository
}

// New creates an intake Service. llm is required; repos are optional.
func New(llm *core.Service, opts ...func(*Service)) *Service {
	s := &Service{llm: llm}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

func WithProfileRepo(r *promptrepo.Repository) func(*Service) {
	return func(s *Service) { s.profileRepo = r }
}

func WithTemplateRepo(r *tplrepo.Repository) func(*Service) {
	return func(s *Service) { s.templateRepo = r }
}

func WithBundleRepo(r *toolbundlerepo.Repository) func(*Service) {
	return func(s *Service) { s.bundleRepo = r }
}

// Run executes the intake sidecar for the given user message and agent config.
// Returns nil when the sidecar is not enabled or a non-fatal error occurs
// (callers always proceed with the turn regardless).
func (s *Service) Run(ctx context.Context, userMessage string, cfg *agentmdl.Intake, userID string) *Context {
	if s == nil || s.llm == nil || cfg == nil || !cfg.Enabled || strings.TrimSpace(userMessage) == "" {
		return nil
	}
	tc, err := s.run(ctx, userMessage, cfg, userID)
	if err != nil {
		logx.Warnf("conversation", "intake.Run error: %v", err)
		return nil
	}
	return tc
}

func (s *Service) run(ctx context.Context, userMessage string, cfg *agentmdl.Intake, userID string) (*Context, error) {
	modelName := s.resolveModel(cfg)
	if modelName == "" {
		return nil, fmt.Errorf("intake: no model configured (set cfg.Model or cfg.ModelPreferences with a matcher available)")
	}

	systemPrompt, err := s.buildSystemPrompt(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("intake: build system prompt: %w", err)
	}

	timeoutSec := cfg.EffectiveTimeoutSec()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	in := s.buildGenerateInputWithContext(ctx, modelName, systemPrompt, userMessage, userID, cfg)
	if strings.TrimSpace(in.UserID) == "" {
		in.UserID = "system"
	}
	out := &core.GenerateOutput{}
	if err := s.llm.Generate(runCtx, in, out); err != nil {
		return nil, fmt.Errorf("intake: generate: %w", err)
	}
	content := strings.TrimSpace(out.Content)
	if out.Response != nil && len(out.Response.Choices) > 0 {
		if c := strings.TrimSpace(out.Response.Choices[0].Message.Content); c != "" {
			content = c
		}
	}
	if content == "" {
		return nil, fmt.Errorf("intake: empty response")
	}
	tc, err := parseOutput(content)
	if err != nil {
		return nil, fmt.Errorf("intake: parse output: %w", err)
	}
	if strings.TrimSpace(tc.Routing.Source) == "" {
		tc.Routing.Source = SourceAgent
	}
	filterByScope(tc, cfg)
	return tc, nil
}

func (s *Service) buildGenerateInput(modelName, systemPrompt, userMessage, userID string, cfg *agentmdl.Intake) *core.GenerateInput {
	return s.buildGenerateInputWithContext(context.Background(), modelName, systemPrompt, userMessage, userID, cfg)
}

func (s *Service) buildGenerateInputWithContext(ctx context.Context, modelName, systemPrompt, userMessage, userID string, cfg *agentmdl.Intake) *core.GenerateInput {
	return &core.GenerateInput{
		UserID: userID,
		ModelSelection: llm.ModelSelection{
			Model: modelName,
			Options: &llm.Options{
				Temperature:      0.0000001,
				MaxTokens:        cfg.EffectiveMaxTokens(),
				JSONMode:         true,
				ResponseMIMEType: "application/json",
				OutputSchema:     s.buildOutputJSONSchema(ctx, cfg),
				ToolChoice:       llm.NewNoneToolChoice(),
				Mode:             "router",
				Reasoning:        &llm.Reasoning{Effort: "minimal"},
			},
		},
		Message: []llm.Message{
			llm.NewSystemMessage(systemPrompt),
			llm.NewUserMessage(userMessage),
		},
	}
}

func (s *Service) buildOutputJSONSchema(ctx context.Context, cfg *agentmdl.Intake) map[string]interface{} {
	schema := buildOutputJSONSchema(cfg)
	if s == nil || cfg == nil {
		return schema
	}
	props, _ := schema["properties"].(map[string]interface{})
	prompting, _ := props["prompting"].(map[string]interface{})
	promptingProps, _ := prompting["properties"].(map[string]interface{})

	if cfg.HasScope(agentmdl.IntakeScopeProfile) {
		if prop, ok := promptingProps["suggestedProfileId"].(map[string]interface{}); ok {
			if enums := s.allowedPromptProfileIDs(ctx); len(enums) > 0 {
				prop["enum"] = append([]string{""}, enums...)
			}
		}
	}
	if cfg.HasScope(agentmdl.IntakeScopeTools) {
		if prop, ok := promptingProps["appendToolBundles"].(map[string]interface{}); ok {
			if items, ok := prop["items"].(map[string]interface{}); ok {
				if enums := s.allowedBundleIDs(ctx); len(enums) > 0 {
					items["enum"] = enums
				}
			}
		}
	}
	if cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		if prop, ok := promptingProps["templateId"].(map[string]interface{}); ok {
			if enums := s.allowedTemplateIDs(ctx); len(enums) > 0 {
				prop["enum"] = append([]string{""}, enums...)
			}
		}
	}
	return schema
}

func (s *Service) allowedPromptProfileIDs(ctx context.Context) []string {
	if s == nil || s.profileRepo == nil {
		return nil
	}
	profiles, err := s.profileRepo.LoadAll(ctx)
	if err != nil || len(profiles) == 0 {
		return nil
	}
	if allow := runtimerequestctx.PromptProfileAllowListFromContext(ctx); len(allow) > 0 {
		profiles = promptrepo.FilterAllowedProfiles(profiles, allow)
	}
	ids := make([]string, 0, len(profiles))
	seen := map[string]bool{}
	for _, p := range profiles {
		if p == nil {
			continue
		}
		id := strings.TrimSpace(p.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) allowedBundleIDs(ctx context.Context) []string {
	if s == nil || s.bundleRepo == nil {
		return nil
	}
	bundles, err := s.bundleRepo.LoadAll(ctx)
	if err != nil || len(bundles) == 0 {
		return nil
	}
	ids := make([]string, 0, len(bundles))
	seen := map[string]bool{}
	for _, b := range bundles {
		if b == nil {
			continue
		}
		id := strings.TrimSpace(b.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Service) allowedTemplateIDs(ctx context.Context) []string {
	if s == nil || s.templateRepo == nil {
		return nil
	}
	templates, err := s.templateRepo.LoadAll(ctx)
	if err != nil || len(templates) == 0 {
		return nil
	}
	ids := make([]string, 0, len(templates))
	seen := map[string]bool{}
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		id := strings.TrimSpace(tpl.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// resolveModel selects a concrete model id for the intake call.
//
// Resolution order:
//
//  1. Explicit cfg.Model when non-empty (matches existing behavior).
//  2. cfg.ModelPreferences via the matcher exposed by core.Service.ModelMatcher()
//     (the existing internal/finder/model.Finder selector). No new abstraction;
//     the matcher already drives `llm/agents:start` and skill activation.
//
// Returns "" when neither path yields a model. Callers treat that as a
// configuration error (intake is skipped/fails per existing semantics).
func (s *Service) resolveModel(cfg *agentmdl.Intake) string {
	if cfg == nil {
		return ""
	}
	if name := strings.TrimSpace(cfg.Model); name != "" {
		return name
	}
	if cfg.ModelPreferences == nil {
		return ""
	}
	if s == nil || s.llm == nil {
		return ""
	}
	matcher := s.llm.ModelMatcher()
	if matcher == nil {
		return ""
	}
	return strings.TrimSpace(matcher.Best(cfg.ModelPreferences))
}

// buildSystemPrompt constructs the sidecar's system instruction, embedding
// profile and bundle metadata when Class B scope is active.
func (s *Service) buildSystemPrompt(ctx context.Context, cfg *agentmdl.Intake) (string, error) {
	var b strings.Builder
	b.WriteString(intakeBasePrompt)
	b.WriteString("\nCurrent local date: ")
	b.WriteString(time.Now().Format("2006-01-02"))
	if extra := strings.TrimSpace(cfg.Prompt); extra != "" {
		b.WriteString("\n\nWorkspace-specific intake guidance:\n")
		b.WriteString(extra)
	}

	hasProfile := cfg.HasScope(agentmdl.IntakeScopeProfile)
	hasTools := cfg.HasScope(agentmdl.IntakeScopeTools)
	hasTemplate := cfg.HasScope(agentmdl.IntakeScopeTemplate)

	if hasProfile && s.profileRepo != nil {
		profiles, err := s.profileRepo.LoadAll(ctx)
		if err == nil && len(profiles) > 0 {
			if allow := runtimerequestctx.PromptProfileAllowListFromContext(ctx); len(allow) > 0 {
				profiles = promptrepo.FilterAllowedProfiles(profiles, allow)
			}
			b.WriteString("\n\nAvailable prompt profiles (id → description → appliesTo tags):\n")
			for _, p := range profiles {
				if p == nil {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s: %s [%s]\n",
					strings.TrimSpace(p.ID),
					strings.TrimSpace(p.Description),
					strings.Join(p.AppliesTo, ", "),
				))
			}
		}
	}

	if hasTools && s.bundleRepo != nil {
		bundles, err := s.bundleRepo.LoadAll(ctx)
		if err == nil && len(bundles) > 0 {
			b.WriteString("\n\nAvailable tool bundles (id → description):\n")
			for _, bun := range bundles {
				if bun == nil {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s: %s\n",
					strings.TrimSpace(bun.ID),
					strings.TrimSpace(bun.Description),
				))
			}
		}
	}

	if hasTemplate && s.templateRepo != nil {
		templates, err := s.templateRepo.LoadAll(ctx)
		if err == nil && len(templates) > 0 {
			b.WriteString("\n\nAvailable output templates (id → description → appliesTo tags):\n")
			for _, tpl := range templates {
				if tpl == nil {
					continue
				}
				b.WriteString(fmt.Sprintf("- %s: %s [%s]\n",
					strings.TrimSpace(tpl.ID),
					strings.TrimSpace(tpl.Description),
					strings.Join(templateAppliesTo(tpl), ", "),
				))
			}
		}
	}

	b.WriteString(buildOutputSchema(cfg))
	return b.String(), nil
}

func templateAppliesTo(tpl *tpldef.Template) []string {
	if tpl == nil {
		return nil
	}
	if len(tpl.AppliesTo) > 0 {
		return tpl.AppliesTo
	}
	return []string{"general"}
}

// buildOutputSchema appends a JSON schema description to the system prompt
// based on which scope fields are active.
func buildOutputSchema(cfg *agentmdl.Intake) string {
	var b strings.Builder
	b.WriteString("\n\nRespond with a single JSON object. Include only the fields listed below:")
	if cfg.HasScope(agentmdl.IntakeScopeIntent) {
		b.WriteString("\n- classification (object): grouped task labeling with `title`, `intent`, and optional `confidence`")
	}
	if cfg.HasScope(agentmdl.IntakeScopeContext) {
		b.WriteString("\n- scope (object): grouped orchestration scope with `values` containing lightweight extracted identifiers and hints")
	}
	if cfg.HasScope(agentmdl.IntakeScopeProfile) {
		b.WriteString("\n- prompting (object): grouped execution hints with `suggestedProfileId`, `appendToolBundles`, and `templateId`")
	}
	if cfg.HasScope(agentmdl.IntakeScopeTools) {
		b.WriteString("\n- prompting.appendToolBundles may be used when additional bundle ids are needed for this task")
	}
	b.WriteString("\n\nReturn ONLY the JSON object. No prose, no markdown fences.")
	return b.String()
}

func buildOutputJSONSchema(cfg *agentmdl.Intake) map[string]interface{} {
	properties := map[string]interface{}{}
	if cfg == nil {
		required := []string{}
		return map[string]interface{}{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		}
	}
	if cfg.HasScope(agentmdl.IntakeScopeIntent) {
		properties["classification"] = map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"title":      map[string]interface{}{"type": "string"},
				"intent":     map[string]interface{}{"type": "string"},
				"confidence": map[string]interface{}{"type": "number"},
			},
			"required":             []string{"title", "intent", "confidence"},
			"additionalProperties": false,
		}
	}
	if cfg.HasScope(agentmdl.IntakeScopeContext) {
		properties["scope"] = map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"values": map[string]interface{}{
					"type":                 "object",
					"properties":           map[string]interface{}{},
					"required":             []string{},
					"additionalProperties": map[string]interface{}{"type": "string"},
				},
			},
			"required":             []string{"values"},
			"additionalProperties": false,
		}
	}
	if cfg.HasScope(agentmdl.IntakeScopeProfile) || cfg.HasScope(agentmdl.IntakeScopeTools) || cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		promptingProps := map[string]interface{}{}
		promptingRequired := make([]string, 0, 3)
		if cfg.HasScope(agentmdl.IntakeScopeProfile) {
			promptingProps["suggestedProfileId"] = map[string]interface{}{"type": "string"}
			promptingRequired = append(promptingRequired, "suggestedProfileId")
		}
		if cfg.HasScope(agentmdl.IntakeScopeTools) {
			promptingProps["appendToolBundles"] = map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "string"},
			}
			promptingRequired = append(promptingRequired, "appendToolBundles")
		}
		if cfg.HasScope(agentmdl.IntakeScopeTemplate) {
			promptingProps["templateId"] = map[string]interface{}{"type": "string"}
			promptingRequired = append(promptingRequired, "templateId")
		}
		properties["prompting"] = map[string]interface{}{
			"type":                 "object",
			"properties":           promptingProps,
			"required":             promptingRequired,
			"additionalProperties": false,
		}
	}
	required := make([]string, 0, len(properties))
	for key := range properties {
		required = append(required, key)
	}
	sort.Strings(required)
	return map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

const intakeBasePrompt = `You are a request classifier that extracts structured metadata from user messages.
Your output drives downstream routing and tool selection — be precise and conservative.
Do not invent context, dates, or constraints not present in the message.
Do not output tool names or capability descriptions.
When you emit string fields such as intent, suggestedProfileId, or templateId:
- return the exact bare string value only
- do not append explanations, comments, punctuation notes, or side remarks
- do not concatenate two values into one field
- never emit inline comments like /* ... */ inside JSON string values

Date rule:
- If the user gives a concrete month/day date without a year (for example
  "3/17", "03/17", "March 17", or "Mar 17") and the message/thread does not
  indicate another year, assume the current year.
- Preserve the resolved date in exact YYYY-MM-DD form inside extracted
  context when you emit a date.

Clarification rule:
- If the message already names a concrete entity scope such as an ad order, campaign, advertiser, audience, repo, package, file path, or other directly actionable object, do not ask for clarification just because timeframe, KPI family, or symptom subtype was omitted.
- For concrete troubleshoot / diagnose / investigate / analyze asks on a named entity, prefer routing directly so the owning agent can establish the baseline from the available tools.
- Ask for clarification only when a missing detail truly blocks selecting any reasonable next step.`

// parseOutput unmarshals the sidecar's JSON output into an intake Context.
func parseOutput(raw string) (*Context, error) {
	raw = stripFence(raw)
	var tc Context
	if err := unmarshalContext([]byte(raw), &tc); err != nil {
		// Lenient: look for first '{' and last '}'
		if start := strings.Index(raw, "{"); start >= 0 {
			if end := strings.LastIndex(raw, "}"); end > start {
				if err2 := unmarshalContext([]byte(raw[start:end+1]), &tc); err2 == nil {
					return &tc, nil
				}
			}
		}
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &tc, nil
}

// filterByScope zeroes out Class B fields that are not in scope.
func filterByScope(tc *Context, cfg *agentmdl.Intake) {
	if tc == nil || cfg == nil {
		return
	}
	if !cfg.HasScope(agentmdl.IntakeScopeIntent) {
		tc.Classification.Intent = ""
	}
	if !cfg.HasScope(agentmdl.IntakeScopeContext) {
		tc.Scope.Values = nil
	}
	if !cfg.HasScope(agentmdl.IntakeScopeProfile) {
		tc.Prompting.SuggestedProfileID = ""
		tc.Classification.Confidence = 0
	}
	if !cfg.HasScope(agentmdl.IntakeScopeTools) {
		tc.Prompting.AppendToolBundles = nil
	}
	if !cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		tc.Prompting.TemplateID = ""
	}
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if nl := strings.Index(s, "\n"); nl >= 0 {
			s = s[nl+1:]
		}
		if strings.HasSuffix(s, "```") {
			s = s[:len(s)-3]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// ensure promptdef import is used (for future MCP-sourced profile metadata)
var _ = promptdef.Profile{}

type contextWire struct {
	Classification     *contextClassificationWire `json:"classification,omitempty"`
	Scope              *contextScopeWire          `json:"scope,omitempty"`
	Prompting          *contextPromptingWire      `json:"prompting,omitempty"`
	Routing            *contextRoutingWire        `json:"routing,omitempty"`
	Planner            *contextPlannerWire        `json:"planner,omitempty"`
	Title              string                     `json:"title,omitempty"`
	Intent             string                     `json:"intent,omitempty"`
	Context            map[string]string          `json:"context,omitempty"`
	Entities           map[string]string          `json:"entities,omitempty"`
	SuggestedProfileId string                     `json:"suggestedProfileId,omitempty"`
	AppendToolBundles  []string                   `json:"appendToolBundles,omitempty"`
	TemplateId         string                     `json:"templateId,omitempty"`
	Confidence         float64                    `json:"confidence,omitempty"`
	// Workspace-intake fields (additive). Legacy agent-intake outputs do not
	// emit these keys; their absence leaves zero-values, which is the correct
	// fallback semantics.
	SelectedAgentID string `json:"selectedAgentId,omitempty"`
	Mode            string `json:"mode,omitempty"`
	PlannerTrigger  string `json:"plannerTrigger,omitempty"`
	PlannerAgentID  string `json:"plannerAgentId,omitempty"`
	Source          string `json:"source,omitempty"`
}

type contextClassificationWire struct {
	Title      string  `json:"title,omitempty"`
	Intent     string  `json:"intent,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type contextScopeWire struct {
	Values map[string]string `json:"values,omitempty"`
}

type contextPromptingWire struct {
	SuggestedProfileID string   `json:"suggestedProfileId,omitempty"`
	AppendToolBundles  []string `json:"appendToolBundles,omitempty"`
	TemplateID         string   `json:"templateId,omitempty"`
}

type contextRoutingWire struct {
	SelectedAgentID string `json:"selectedAgentId,omitempty"`
	Mode            string `json:"mode,omitempty"`
	Source          string `json:"source,omitempty"`
}

type contextPlannerWire struct {
	Trigger string `json:"trigger,omitempty"`
	AgentID string `json:"agentId,omitempty"`
}

func unmarshalContext(data []byte, tc *Context) error {
	var wire contextWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Classification != nil {
		tc.Classification.Title = wire.Classification.Title
		tc.Classification.Intent = wire.Classification.Intent
		tc.Classification.Confidence = wire.Classification.Confidence
	}
	if tc.Classification.Title == "" {
		tc.Classification.Title = wire.Title
	}
	if tc.Classification.Intent == "" {
		tc.Classification.Intent = wire.Intent
	}
	if tc.Classification.Confidence == 0 {
		tc.Classification.Confidence = wire.Confidence
	}

	if wire.Scope != nil && len(wire.Scope.Values) > 0 {
		tc.Scope.Values = wire.Scope.Values
	}
	if len(tc.Scope.Values) == 0 {
		tc.Scope.Values = wire.Context
	}
	if len(tc.Scope.Values) == 0 && len(wire.Entities) > 0 {
		tc.Scope.Values = wire.Entities
	}

	if wire.Prompting != nil {
		tc.Prompting.SuggestedProfileID = wire.Prompting.SuggestedProfileID
		tc.Prompting.AppendToolBundles = wire.Prompting.AppendToolBundles
		tc.Prompting.TemplateID = wire.Prompting.TemplateID
	}
	if tc.Prompting.SuggestedProfileID == "" {
		tc.Prompting.SuggestedProfileID = wire.SuggestedProfileId
	}
	if len(tc.Prompting.AppendToolBundles) == 0 {
		tc.Prompting.AppendToolBundles = wire.AppendToolBundles
	}
	if tc.Prompting.TemplateID == "" {
		tc.Prompting.TemplateID = wire.TemplateId
	}

	if wire.Routing != nil {
		tc.Routing.SelectedAgentID = wire.Routing.SelectedAgentID
		tc.Routing.Mode = wire.Routing.Mode
		tc.Routing.Source = wire.Routing.Source
	}
	if tc.Routing.SelectedAgentID == "" {
		tc.Routing.SelectedAgentID = wire.SelectedAgentID
	}
	if tc.Routing.Mode == "" {
		tc.Routing.Mode = wire.Mode
	}
	if tc.Routing.Source == "" {
		tc.Routing.Source = wire.Source
	}

	if wire.Planner != nil {
		tc.Planner.Trigger = wire.Planner.Trigger
		tc.Planner.AgentID = wire.Planner.AgentID
	}
	if tc.Planner.Trigger == "" {
		tc.Planner.Trigger = wire.PlannerTrigger
	}
	if tc.Planner.AgentID == "" {
		tc.Planner.AgentID = wire.PlannerAgentID
	}
	return nil
}
