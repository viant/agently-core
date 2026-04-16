package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/internal/logx"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	"github.com/viant/agently-core/service/core"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
	toolbundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
)

// Service runs the intake sidecar LLM call and returns a TurnContext.
type Service struct {
	llm         *core.Service
	profileRepo *promptrepo.Repository
	bundleRepo  *toolbundlerepo.Repository
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

func WithBundleRepo(r *toolbundlerepo.Repository) func(*Service) {
	return func(s *Service) { s.bundleRepo = r }
}

// Run executes the intake sidecar for the given user message and agent config.
// Returns nil when the sidecar is not enabled or a non-fatal error occurs
// (callers always proceed with the turn regardless).
func (s *Service) Run(ctx context.Context, userMessage string, cfg *agentmdl.Intake, userID string) *TurnContext {
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

func (s *Service) run(ctx context.Context, userMessage string, cfg *agentmdl.Intake, userID string) (*TurnContext, error) {
	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		return nil, fmt.Errorf("intake: no model configured")
	}

	systemPrompt, err := s.buildSystemPrompt(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("intake: build system prompt: %w", err)
	}

	timeoutSec := cfg.EffectiveTimeoutSec()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	in := &core.GenerateInput{
		UserID: userID,
		ModelSelection: llm.ModelSelection{
			Model: modelName,
			Options: &llm.Options{
				Temperature:      0.0000001,
				MaxTokens:        cfg.EffectiveMaxTokens(),
				JSONMode:         true,
				ResponseMIMEType: "application/json",
				ToolChoice:       llm.NewNoneToolChoice(),
				Mode:             "router",
			},
		},
		Message: []llm.Message{
			llm.NewSystemMessage(systemPrompt),
			llm.NewUserMessage(userMessage),
		},
	}
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
	filterByScope(tc, cfg)
	return tc, nil
}

// buildSystemPrompt constructs the sidecar's system instruction, embedding
// profile and bundle metadata when Class B scope is active.
func (s *Service) buildSystemPrompt(ctx context.Context, cfg *agentmdl.Intake) (string, error) {
	var b strings.Builder
	b.WriteString(intakeBasePrompt)

	hasProfile := cfg.HasScope(agentmdl.IntakeScopeProfile)
	hasTools := cfg.HasScope(agentmdl.IntakeScopeTools)

	if hasProfile && s.profileRepo != nil {
		profiles, err := s.profileRepo.LoadAll(ctx)
		if err == nil && len(profiles) > 0 {
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

	b.WriteString(buildOutputSchema(cfg))
	return b.String(), nil
}

// buildOutputSchema appends a JSON schema description to the system prompt
// based on which scope fields are active.
func buildOutputSchema(cfg *agentmdl.Intake) string {
	var b strings.Builder
	b.WriteString("\n\nRespond with a single JSON object. Include only the fields listed below:")
	b.WriteString("\n- title (string, ≤80 chars): concise label for the user's task")
	if cfg.HasScope(agentmdl.IntakeScopeIntent) {
		b.WriteString("\n- intent (string): one-word classification of goal, e.g. diagnosis, comparison, summary, configuration")
	}
	if cfg.HasScope(agentmdl.IntakeScopeEntities) {
		b.WriteString("\n- entities (object): key domain objects extracted from the request")
	}
	if cfg.HasScope(agentmdl.IntakeScopeClarification) {
		b.WriteString("\n- clarificationNeeded (boolean): true if request is too ambiguous to act on")
		b.WriteString("\n- clarificationQuestion (string): question to ask when clarificationNeeded is true")
	}
	if cfg.HasScope(agentmdl.IntakeScopeProfile) {
		b.WriteString("\n- suggestedProfileId (string): id of the most relevant prompt profile from the list above, or omit")
		b.WriteString("\n- confidence (number 0–1): your confidence in the suggestedProfileId")
	}
	if cfg.HasScope(agentmdl.IntakeScopeTools) {
		b.WriteString("\n- appendToolBundles (array of strings): additional bundle ids needed for this task")
	}
	if cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		b.WriteString("\n- templateId (string): output template id if user phrasing implies a specific format, or omit")
	}
	b.WriteString("\n\nReturn ONLY the JSON object. No prose, no markdown fences.")
	return b.String()
}

const intakeBasePrompt = `You are a request classifier that extracts structured metadata from user messages.
Your output drives downstream routing and tool selection — be precise and conservative.
Do not invent entities, dates, or constraints not present in the message.
Do not output tool names or capability descriptions.`

// parseOutput unmarshals the sidecar's JSON output into a TurnContext.
func parseOutput(raw string) (*TurnContext, error) {
	raw = stripFence(raw)
	var tc TurnContext
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		// Lenient: look for first '{' and last '}'
		if start := strings.Index(raw, "{"); start >= 0 {
			if end := strings.LastIndex(raw, "}"); end > start {
				if err2 := json.Unmarshal([]byte(raw[start:end+1]), &tc); err2 == nil {
					return &tc, nil
				}
			}
		}
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &tc, nil
}

// filterByScope zeroes out Class B fields that are not in scope.
func filterByScope(tc *TurnContext, cfg *agentmdl.Intake) {
	if tc == nil || cfg == nil {
		return
	}
	if !cfg.HasScope(agentmdl.IntakeScopeIntent) {
		tc.Intent = ""
	}
	if !cfg.HasScope(agentmdl.IntakeScopeEntities) {
		tc.Entities = nil
	}
	if !cfg.HasScope(agentmdl.IntakeScopeClarification) {
		tc.ClarificationNeeded = false
		tc.ClarificationQuestion = ""
	}
	if !cfg.HasScope(agentmdl.IntakeScopeProfile) {
		tc.SuggestedProfileId = ""
		tc.Confidence = 0
	}
	if !cfg.HasScope(agentmdl.IntakeScopeTools) {
		tc.AppendToolBundles = nil
	}
	if !cfg.HasScope(agentmdl.IntakeScopeTemplate) {
		tc.TemplateId = ""
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
