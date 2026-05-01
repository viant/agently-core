package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	mcpname "github.com/viant/agently-core/pkg/mcpname"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	skillproto "github.com/viant/agently-core/protocol/skill"
	"github.com/viant/agently-core/protocol/tool"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/runtime/streaming"
	skillrepo "github.com/viant/agently-core/workspace/repository/skill"
)

const Name = "llm/skills"

type activationModeOverrideKey struct{}

type activationModeOverride struct {
	name string
	mode string
}

type nestedToolCallRecord struct {
	ToolMessageID string
	ToolCallID    string
	ParentMessage string
}

type Service struct {
	defaults    *execconfig.Defaults
	conv        apiconv.Client
	agentFinder agentmdl.Finder
	mu          sync.RWMutex
	registry    *skillproto.Registry
	loader      *skillrepo.Loader
	budgetChars int
	streamPub   streaming.Publisher
}

func New(defaults *execconfig.Defaults, conv apiconv.Client, finder agentmdl.Finder) *Service {
	return &Service{
		defaults:    defaults,
		conv:        conv,
		agentFinder: finder,
		loader:      skillrepo.New(defaults),
		budgetChars: skillproto.DefaultPromptBudgetChars,
	}
}

func (s *Service) Load(ctx context.Context) error {
	reg, err := s.loader.LoadAll()
	if err != nil {
		return err
	}
	// Snapshot the prior registry's name+fingerprint set BEFORE the swap so
	// we can compute a {added, changed, removed} diff for the watcher event.
	// First load (s.registry == nil) reports every name as "added".
	priorFingerprints := s.skillFingerprints()

	s.mu.Lock()
	s.registry = reg
	s.mu.Unlock()

	added, changed, removed := diffRegistries(priorFingerprints, s.skillFingerprints())
	s.publishRegistryUpdatedWithDiff(ctx, added, changed, removed)
	return nil
}

func (s *Service) SetStreamPublisher(p streaming.Publisher) {
	if s == nil {
		return
	}
	s.streamPub = p
}

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List visible skills for the current agent", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "activate", Description: "Activate one skill. Inline skills return their body for the current turn; fork/detach skills may already start a child conversation and return its execution state. When started=true and childConversationId is set, do not launch another agent into that conversation; poll llm/agents:status on the returned childConversationId instead. Optional input.mode may override the skill's default mode with inline, fork, or detach.", Input: reflect.TypeOf(&ActivateInput{}), Output: reflect.TypeOf(&ActivateOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "activate":
		return s.activate, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

type ListInput struct{}
type ListOutput struct {
	Items       []skillproto.Metadata `json:"items,omitempty"`
	Diagnostics []string              `json:"diagnostics,omitempty"`
}
type ActivateInput struct {
	Name string `json:"name,omitempty"`
	Args string `json:"args,omitempty"`
	Mode string `json:"mode,omitempty"`
}
type ActivateOutput struct {
	Name                string `json:"name,omitempty"`
	Body                string `json:"body,omitempty"`
	Mode                string `json:"mode,omitempty"`
	Started             bool   `json:"started,omitempty"`
	Terminal            bool   `json:"terminal,omitempty"`
	Status              string `json:"status,omitempty"`
	ChildConversationID string `json:"childConversationId,omitempty"`
	ChildAgentID        string `json:"childAgentId,omitempty"`
}

func (s *Service) Visible(agent *agentmdl.Agent) ([]skillproto.Metadata, string) {
	skills := s.visibleSkills(agent)
	meta := make([]skillproto.Metadata, 0, len(skills))
	for _, item := range skills {
		meta = append(meta, skillproto.Metadata{
			Name:          item.Frontmatter.Name,
			Description:   item.Frontmatter.Description,
			ExecutionMode: item.Frontmatter.ContextMode(),
		})
	}
	return meta, skillproto.RenderPrompt(meta, s.budgetChars)
}

func (s *Service) visibleSkills(agent *agentmdl.Agent) []*skillproto.Skill {
	if s == nil || s.registry == nil || agent == nil || len(agent.Skills) == 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var positives, negatives []string
	for _, entry := range agent.Skills {
		v := strings.TrimSpace(entry)
		if v == "" {
			continue
		}
		if strings.HasPrefix(v, "!") {
			negatives = append(negatives, strings.TrimSpace(v[1:]))
		} else {
			positives = append(positives, v)
		}
	}
	if len(positives) == 0 {
		return nil
	}
	selected := map[string]*skillproto.Skill{}
	for _, item := range s.registry.List() {
		if item == nil {
			continue
		}
		name := strings.TrimSpace(item.Frontmatter.Name)
		for _, pattern := range positives {
			if skillPatternMatch(name, pattern) {
				selected[name] = item
				break
			}
		}
	}
	for name := range selected {
		for _, pattern := range negatives {
			if skillPatternMatch(name, pattern) {
				delete(selected, name)
				break
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*skillproto.Skill, 0, len(names))
	for _, name := range names {
		out = append(out, selected[name])
	}
	return out
}

func skillPatternMatch(name, pattern string) bool {
	name = strings.TrimSpace(name)
	pattern = strings.TrimSpace(pattern)
	switch {
	case pattern == "*":
		return true
	case strings.HasPrefix(pattern, "*") && strings.HasSuffix(name, strings.TrimPrefix(pattern, "*")):
		return true
	case strings.HasSuffix(pattern, "*") && strings.HasPrefix(name, strings.TrimSuffix(pattern, "*")):
		return true
	default:
		return name == pattern
	}
}

func (s *Service) findVisibleSkill(agent *agentmdl.Agent, name string) (*skillproto.Skill, error) {
	name = strings.TrimSpace(name)
	for _, item := range s.visibleSkills(agent) {
		if item != nil && strings.EqualFold(strings.TrimSpace(item.Frontmatter.Name), name) {
			return item, nil
		}
	}
	return nil, fmt.Errorf("skill not available: %s", name)
}

func (s *Service) Resolve(agent *agentmdl.Agent, name string) (*skillproto.Skill, error) {
	if agent != nil {
		return s.findVisibleSkill(agent, name)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("skill name is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return nil, fmt.Errorf("skill registry not loaded")
	}
	for _, item := range s.registry.List() {
		if item != nil && strings.EqualFold(strings.TrimSpace(item.Frontmatter.Name), name) {
			return item, nil
		}
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

func (s *Service) VisibleSkillsByName(agent *agentmdl.Agent, names []string) []*skillproto.Skill {
	if len(names) == 0 {
		return nil
	}
	visible := s.visibleSkills(agent)
	if len(visible) == 0 {
		return nil
	}
	index := map[string]*skillproto.Skill{}
	for _, item := range visible {
		if item != nil {
			index[strings.TrimSpace(strings.ToLower(item.Frontmatter.Name))] = item
		}
	}
	var out []*skillproto.Skill
	seen := map[string]struct{}{}
	for _, name := range names {
		key := strings.TrimSpace(strings.ToLower(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		if item, ok := index[key]; ok && item != nil {
			out = append(out, item)
			seen[key] = struct{}{}
		}
	}
	return out
}

func (s *Service) Activate(agent *agentmdl.Agent, name, args string) (string, error) {
	body, _, err := s.activateWithContext(context.Background(), agent, name, args)
	return body, err
}

func (s *Service) activateWithContext(ctx context.Context, agent *agentmdl.Agent, name, args string) (string, preprocessStats, error) {
	item, err := s.findVisibleSkill(agent, name)
	if err != nil {
		return "", preprocessStats{}, err
	}
	return s.activateResolvedWithContext(ctx, item, args)
}

func (s *Service) activateResolvedWithContext(ctx context.Context, item *skillproto.Skill, args string) (string, preprocessStats, error) {
	if item == nil {
		return "", preprocessStats{}, fmt.Errorf("skill is required")
	}
	body := strings.TrimSpace(item.Body)
	name := strings.TrimSpace(item.Frontmatter.Name)
	if body == "" {
		return "", preprocessStats{}, fmt.Errorf("skill %q has empty body", name)
	}
	stats := preprocessStats{}
	if item.Frontmatter.PreprocessEnabled() {
		var diags []string
		body, diags, stats = preprocessBody(ctx, body, item, strings.TrimSpace(args), preprocessConversationID(ctx))
		if len(diags) > 0 {
			body = body + "\n\nDiagnostics:\n- " + strings.Join(diags, "\n- ")
		}
	}
	text := fmt.Sprintf("Loaded skill %q. Follow the instructions below:\n\n%s", strings.TrimSpace(item.Frontmatter.Name), body)
	if v := strings.TrimSpace(augmentSkillArgsWithRuntimeClock(strings.TrimSpace(args))); v != "" {
		text += "\n\nArguments:\n" + v
	}
	return text, stats, nil
}

type agentsStartOutput struct {
	ConversationID string `json:"conversationId,omitempty"`
	Status         string `json:"status,omitempty"`
	Message        string `json:"message,omitempty"`
	MessageKind    string `json:"messageKind,omitempty"`
}

type agentsStatusOutput struct {
	ConversationID string `json:"conversationId,omitempty"`
	Status         string `json:"status,omitempty"`
	Terminal       bool   `json:"terminal,omitempty"`
	Error          string `json:"error,omitempty"`
	Message        string `json:"message,omitempty"`
	MessageKind    string `json:"messageKind,omitempty"`
}

func explicitSkillObjective(name, args string) string {
	command := "/" + strings.TrimSpace(name)
	if v := strings.TrimSpace(augmentSkillArgsWithRuntimeClock(strings.TrimSpace(args))); v != "" {
		return command + " " + v
	}
	return command
}

func (s *Service) delegatedSkillObjective(ctx context.Context, name, args string) string {
	if strings.TrimSpace(args) != "" {
		return explicitSkillObjective(name, args)
	}
	if s != nil && s.conv != nil {
		if conversationID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); conversationID != "" {
			if fallback := strings.TrimSpace(s.latestUserTask(ctx, conversationID)); fallback != "" {
				return fallback
			}
		}
	}
	return explicitSkillObjective(name, args)
}

func (s *Service) latestUserTask(ctx context.Context, conversationID string) string {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID), apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return ""
	}
	tr := conv.GetTranscript()
	for ti := len(tr) - 1; ti >= 0; ti-- {
		turn := tr[ti]
		if turn == nil {
			continue
		}
		msgs := turn.GetMessages()
		for mi := len(msgs) - 1; mi >= 0; mi-- {
			msg := msgs[mi]
			if msg == nil || msg.Content == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(msg.Role)) != "user" {
				continue
			}
			content := strings.TrimSpace(*msg.Content)
			if content != "" {
				return content
			}
		}
	}
	return ""
}

func dynamicSkillAgentID(parent *agentmdl.Agent, item *skillproto.Skill) string {
	if item != nil {
		if id := strings.TrimSpace(item.Frontmatter.AgentIDValue()); id != "" {
			return id
		}
	}
	skillName := ""
	if item != nil {
		skillName = strings.TrimSpace(item.Frontmatter.Name)
	}
	parentID := ""
	if parent != nil {
		parentID = strings.TrimSpace(parent.ID)
	}
	switch {
	case parentID != "" && skillName != "":
		return parentID + "/" + skillName
	case skillName != "":
		return "skill/" + skillName
	case parentID != "":
		return parentID + "/skill-child"
	default:
		return "skill/child"
	}
}

func dynamicSkillAgentName(parent *agentmdl.Agent, item *skillproto.Skill) string {
	skillName := ""
	if item != nil {
		skillName = strings.TrimSpace(item.Frontmatter.Name)
	}
	parentName := ""
	if parent != nil {
		parentName = strings.TrimSpace(parent.Name)
	}
	label := strings.ReplaceAll(skillName, "-", " ")
	label = strings.TrimSpace(label)
	label = strings.Title(label)
	switch {
	case parentName != "" && label != "":
		return parentName + " / " + label
	case label != "":
		return label
	case parentName != "":
		return parentName + " / Skill Child"
	default:
		return "Skill Child"
	}
}

func dynamicSkillSystemPrompt(item *skillproto.Skill, loadedBody string) string {
	name := ""
	if item != nil {
		name = strings.TrimSpace(item.Frontmatter.Name)
	}
	body := strings.TrimSpace(loadedBody)
	parts := []string{
		fmt.Sprintf("You are a child agent derived dynamically from the %q skill.", name),
		"Rules:",
		"- Execute only the delegated skill task.",
		"- Do not perform broad orchestration or re-route the task elsewhere.",
		"- Treat the rest of this system prompt as the authoritative skill contract.",
	}
	if body != "" {
		parts = append(parts, "", body)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func deriveDynamicSkillAgent(parent *agentmdl.Agent, item *skillproto.Skill, loadedBody string) *agentmdl.Agent {
	if item == nil {
		return nil
	}
	var toolItems []*llm.Tool
	for _, token := range skillproto.ParseAllowedTools(item.Frontmatter.AllowedTools) {
		switch {
		case strings.TrimSpace(token.ToolPattern) != "":
			toolItems = append(toolItems, &llm.Tool{Name: strings.TrimSpace(token.ToolPattern)})
		case strings.TrimSpace(token.BashCommand) != "":
			toolItems = append(toolItems, &llm.Tool{Name: "system/exec:execute"})
		}
	}
	derived := &agentmdl.Agent{
		Identity: agentmdl.Identity{
			ID:   dynamicSkillAgentID(parent, item),
			Name: dynamicSkillAgentName(parent, item),
		},
		Internal:     true,
		Description:  fmt.Sprintf("Dynamic child agent derived from skill %q.", strings.TrimSpace(item.Frontmatter.Name)),
		Prompt:       &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		SystemPrompt: &binding.Prompt{Text: dynamicSkillSystemPrompt(item, loadedBody), Engine: "go"},
		Persona:      &binding.Persona{Role: "assistant", Actor: "Specialist"},
		Source:       &agentmdl.Source{URL: "internal://skill-derived/" + strings.TrimSpace(item.Frontmatter.Name)},
		Tool: agentmdl.Tool{
			Items: toolItems,
		},
	}
	if parent != nil {
		derived.ToolCallExposure = parent.ToolCallExposure
		derived.Tool.CallExposure = parent.Tool.CallExposure
		derived.DefaultWorkdir = strings.TrimSpace(parent.DefaultWorkdir)
		if parent.Attachment != nil {
			attachment := *parent.Attachment
			derived.Attachment = &attachment
		}
		derived.ModelSelection = parent.ModelSelection
		derived.AllowedProviders = append([]string{}, parent.AllowedProviders...)
		derived.AllowedModels = append([]string{}, parent.AllowedModels...)
		if parent.Reasoning != nil {
			reasoning := *parent.Reasoning
			derived.Reasoning = &reasoning
		}
		derived.AsyncNarratorPrompt = strings.TrimSpace(parent.AsyncNarratorPrompt)
		if parent.ParallelToolCalls != nil {
			value := *parent.ParallelToolCalls
			derived.ParallelToolCalls = &value
		}
		derived.Temperature = parent.Temperature
	}
	if temp := item.Frontmatter.TemperatureValue(); temp != nil {
		derived.Temperature = *temp
	}
	if maxTokens := item.Frontmatter.MaxTokensValue(); maxTokens > 0 {
		if derived.ModelSelection.Options == nil {
			derived.ModelSelection.Options = &llm.Options{}
		}
		derived.ModelSelection.Options.MaxTokens = maxTokens
	}
	if model := strings.TrimSpace(item.Frontmatter.ModelValue()); model != "" {
		derived.ModelSelection.Model = model
	}
	if effort := strings.TrimSpace(item.Frontmatter.EffortValue()); effort != "" {
		if derived.Reasoning == nil {
			derived.Reasoning = &llm.Reasoning{}
		}
		derived.Reasoning.Effort = effort
	}
	if prompt := strings.TrimSpace(item.Frontmatter.AsyncNarratorPromptValue()); prompt != "" {
		derived.AsyncNarratorPrompt = prompt
	}
	return derived
}

func augmentSkillArgsWithRuntimeClock(args string) string {
	now := time.Now()
	date := now.Format("2006-01-02")
	zone := strings.TrimSpace(now.Location().String())
	if zone == "" {
		zone = "Local"
	}
	context := fmt.Sprintf("Runtime date context:\n- current_date: %s\n- timezone: %s", date, zone)
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return context
	}
	return trimmed + "\n\n" + context
}

func WithActivationModeOverride(ctx context.Context, name, mode string) context.Context {
	mode = skillproto.NormalizeContextMode(mode)
	name = strings.ToLower(strings.TrimSpace(name))
	if ctx == nil || name == "" || mode == "" {
		return ctx
	}
	return context.WithValue(ctx, activationModeOverrideKey{}, activationModeOverride{name: name, mode: mode})
}

func effectiveActivationMode(ctx context.Context, item *skillproto.Skill) string {
	if item == nil {
		return "inline"
	}
	if ctx != nil {
		if override, ok := ctx.Value(activationModeOverrideKey{}).(activationModeOverride); ok {
			if override.name == strings.ToLower(strings.TrimSpace(item.Frontmatter.Name)) {
				return skillproto.NormalizeContextMode(override.mode)
			}
		}
	}
	return item.Frontmatter.ContextMode()
}

func parseAgentsStartOutput(serialized string) (*agentsStartOutput, error) {
	out := &agentsStartOutput{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(serialized)), out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseAgentsStatusOutput(serialized string) (*agentsStatusOutput, error) {
	out := &agentsStatusOutput{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(serialized)), out); err != nil {
		return nil, err
	}
	return out, nil
}

func lastAssistantFinalContent(transcript apiconv.Transcript) string {
	var last string
	for _, turn := range transcript {
		if turn == nil {
			continue
		}
		for _, msg := range turn.Message {
			if msg == nil || strings.ToLower(strings.TrimSpace(msg.Role)) != "assistant" {
				continue
			}
			if msg.Interim != 0 {
				continue
			}
			if text := strings.TrimSpace(ptrString(msg.Content)); text != "" {
				last = text
			}
		}
	}
	return strings.TrimSpace(last)
}

func ptrString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) childConversationFinalBody(ctx context.Context, childConversationID string) string {
	if s == nil || s.conv == nil || strings.TrimSpace(childConversationID) == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(childConversationID), apiconv.WithIncludeTranscript(true))
	if err != nil || conv == nil {
		return ""
	}
	return lastAssistantFinalContent(conv.GetTranscript())
}

func (s *Service) awaitChildConversationTerminal(ctx context.Context, childConversationID string) (*agentsStatusOutput, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		statusSerialized, err := ExecFn(ctx, "llm/agents:status", map[string]interface{}{"conversationId": childConversationID})
		if err != nil {
			return nil, err
		}
		statusOut, err := parseAgentsStatusOutput(statusSerialized)
		if err != nil {
			return nil, fmt.Errorf("parse llm/agents:status output: %w", err)
		}
		if statusOut.Terminal {
			return statusOut, nil
		}
		if time.Now().After(deadline) {
			return statusOut, nil
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// ErrForkCapabilityUnavailable signals that a skill requested fork or detach
// activation but the runtime executor (ExecFn) is not bound. ExecFn is the
// single function-pointer dependency for both `llm/agents:start` (kicks off
// the child conversation) and `llm/agents:status` (polls for terminal
// state); when nil, neither operation can run, so the skill cannot execute
// in a child context.
//
// Callers receiving this error can degrade to inline activation, surface a
// configuration warning to the user, or fail the turn — runtime policy
// decision. The error is detectable via errors.Is so it survives wrapping.
var ErrForkCapabilityUnavailable = errors.New(
	"skill requested fork/detach but ExecFn (used for both llm/agents:start and llm/agents:status) is not bound")

func (s *Service) activateChildConversation(ctx context.Context, agent *agentmdl.Agent, item *skillproto.Skill, args, mode string) (string, map[string]interface{}, error) {
	if ExecFn == nil {
		// Wrap the typed sentinel with the requested mode for context;
		// errors.Is(err, ErrForkCapabilityUnavailable) still returns true.
		return "", nil, fmt.Errorf("requested mode=%q: %w", strings.TrimSpace(mode), ErrForkCapabilityUnavailable)
	}
	targetAgent := deriveDynamicSkillAgent(agent, item, "")
	targetAgentID := ""
	if targetAgent != nil {
		targetAgentID = strings.TrimSpace(targetAgent.ID)
	}
	if targetAgentID == "" {
		return "", nil, fmt.Errorf("skill %q requires an agent identity for %s execution", strings.TrimSpace(item.Frontmatter.Name), item.Frontmatter.ContextMode())
	}
	loadedBody, stats, err := s.activateResolvedWithContext(ctx, item, args)
	if err != nil {
		return "", nil, err
	}
	targetAgent = deriveDynamicSkillAgent(agent, item, loadedBody)
	targetAgentID = strings.TrimSpace(targetAgent.ID)
	startPayload := map[string]interface{}{
		"agentId":       targetAgentID,
		"agent":         targetAgent,
		"objective":     s.delegatedSkillObjective(ctx, item.Frontmatter.Name, args),
		"executionMode": mode,
		"context": map[string]interface{}{
			"skillActivationName":     strings.TrimSpace(item.Frontmatter.Name),
			"skillActivationMode":     mode,
			"skillActivationArgs":     strings.TrimSpace(args),
			"skillActivationBody":     strings.TrimSpace(loadedBody),
			"skillActivationEmbedded": true,
		},
	}
	nestedCall, _ := s.startNestedToolCall(ctx, "llm/agents:start", startPayload)
	serialized, err := ExecFn(ctx, "llm/agents:start", startPayload)
	if err != nil {
		s.finishNestedToolCall(ctx, nestedCall, "llm/agents:start", "failed", err.Error(), "")
		return "", nil, err
	}
	startOut, err := parseAgentsStartOutput(serialized)
	if err != nil {
		s.finishNestedToolCall(ctx, nestedCall, "llm/agents:start", "failed", err.Error(), "")
		return "", nil, fmt.Errorf("parse llm/agents:start output: %w", err)
	}
	childConversationID := strings.TrimSpace(startOut.ConversationID)
	if childConversationID == "" {
		s.finishNestedToolCall(ctx, nestedCall, "llm/agents:start", "failed", fmt.Sprintf("llm/agents:start returned empty conversationId for skill %q", strings.TrimSpace(item.Frontmatter.Name)), serialized)
		return "", nil, fmt.Errorf("llm/agents:start returned empty conversationId for skill %q", strings.TrimSpace(item.Frontmatter.Name))
	}
	s.finishNestedToolCall(ctx, nestedCall, "llm/agents:start", "completed", "", serialized)
	s.emitNestedLinkedConversation(ctx, nestedCall, childConversationID, targetAgentID, targetAgent.Name)
	eventArgs := map[string]interface{}{
		"childAgentId":        targetAgentID,
		"executionMode":       mode,
		"childConversationId": childConversationID,
		"childStatus":         strings.TrimSpace(startOut.Status),
	}
	if stats.CommandsRun > 0 || stats.Denied > 0 || stats.TimedOut > 0 || stats.BytesExpanded > 0 {
		eventArgs["preprocess"] = map[string]interface{}{
			"commandsRun":   stats.CommandsRun,
			"denied":        stats.Denied,
			"timedOut":      stats.TimedOut,
			"bytesExpanded": stats.BytesExpanded,
		}
	}
	if mode == "detach" {
		text := fmt.Sprintf("Activated skill %q in detached child conversation %q. Poll with llm/agents:status using conversationId=%q.", strings.TrimSpace(item.Frontmatter.Name), childConversationID, childConversationID)
		return text, eventArgs, nil
	}
	statusOut, err := s.awaitChildConversationTerminal(ctx, childConversationID)
	if err != nil {
		return "", nil, err
	}
	eventArgs["childStatus"] = strings.TrimSpace(statusOut.Status)
	eventArgs["childTerminal"] = statusOut.Terminal
	if text := strings.TrimSpace(statusOut.Error); text != "" {
		eventArgs["childError"] = text
	}
	body := strings.TrimSpace(s.childConversationFinalBody(ctx, childConversationID))
	if body == "" && strings.TrimSpace(statusOut.MessageKind) == "response" {
		body = strings.TrimSpace(statusOut.Message)
	}
	if body == "" {
		if text := strings.TrimSpace(statusOut.Error); text != "" {
			body = text
		} else {
			body = fmt.Sprintf("Skill %q child conversation %q finished with status %q.", strings.TrimSpace(item.Frontmatter.Name), childConversationID, strings.TrimSpace(statusOut.Status))
		}
	}
	return body, eventArgs, nil
}

func (s *Service) startNestedToolCall(ctx context.Context, toolName string, args map[string]interface{}) (*nestedToolCallRecord, error) {
	if s == nil || s.conv == nil {
		return nil, nil
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return nil, nil
	}
	parentMessageID := strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	if parentMessageID == "" {
		parentMessageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	}
	if parentMessageID == "" {
		parentMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	startedAt := time.Now()
	toolMsgID := uuid.New().String()
	opts := []apiconv.MessageOption{
		apiconv.WithId(toolMsgID),
		apiconv.WithRole("tool"),
		apiconv.WithType("tool_op"),
		apiconv.WithStatus("running"),
		apiconv.WithCreatedAt(startedAt),
		apiconv.WithToolName(mcpname.Display(toolName)),
	}
	if parentMessageID != "" {
		opts = append(opts, apiconv.WithParentMessageID(parentMessageID))
	}
	if runMeta, ok := runtimerequestctx.RunMetaFromContext(ctx); ok && runMeta.Iteration > 0 {
		opts = append(opts, apiconv.WithIteration(runMeta.Iteration))
	}
	if _, err := apiconv.AddMessage(ctx, s.conv, &turn, opts...); err != nil {
		return nil, err
	}
	toolCallID := "skill-child:" + uuid.NewString()
	tc := apiconv.NewToolCall()
	tc.SetMessageID(toolMsgID)
	tc.SetOpID(toolCallID)
	tc.SetToolName(mcpname.Display(toolName))
	tc.SetToolKind("general")
	tc.SetStatus("running")
	tc.SetTurnID(turn.TurnID)
	now := startedAt
	tc.StartedAt = &now
	tc.Has.StartedAt = true
	if err := s.conv.PatchToolCall(ctx, tc); err != nil {
		return nil, err
	}
	if len(args) > 0 {
		_ = s.attachNestedRequestPayload(ctx, toolMsgID, args)
	}
	record := &nestedToolCallRecord{
		ToolMessageID: toolMsgID,
		ToolCallID:    toolCallID,
		ParentMessage: parentMessageID,
	}
	s.publishNestedToolLifecycleEvent(ctx, record, toolName, streaming.EventTypeToolCallStarted, "running", "", "", args)
	return record, nil
}

func (s *Service) finishNestedToolCall(ctx context.Context, rec *nestedToolCallRecord, toolName, status, errMsg, responseBody string) {
	if s == nil || s.conv == nil || rec == nil || strings.TrimSpace(rec.ToolMessageID) == "" {
		return
	}
	respPayloadID := ""
	if strings.TrimSpace(responseBody) != "" {
		if id, err := s.createInlinePayload(ctx, "tool_response", "text/plain", []byte(strings.TrimSpace(responseBody))); err == nil {
			respPayloadID = id
		}
		_ = s.conv.PatchMessage(ctx, func() *apiconv.MutableMessage {
			upd := apiconv.NewMessage()
			upd.SetId(rec.ToolMessageID)
			upd.SetContent(strings.TrimSpace(responseBody))
			return upd
		}())
	}
	updTC := apiconv.NewToolCall()
	updTC.SetMessageID(rec.ToolMessageID)
	updTC.SetOpID(rec.ToolCallID)
	updTC.SetToolName(mcpname.Display(toolName))
	updTC.SetStatus(status)
	done := time.Now()
	updTC.CompletedAt = &done
	updTC.Has.CompletedAt = true
	if respPayloadID != "" {
		updTC.ResponsePayloadID = &respPayloadID
		updTC.Has.ResponsePayloadID = true
	}
	if strings.TrimSpace(errMsg) != "" {
		errCopy := strings.TrimSpace(errMsg)
		updTC.ErrorMessage = &errCopy
		updTC.Has.ErrorMessage = true
		_ = s.conv.PatchMessage(ctx, func() *apiconv.MutableMessage {
			upd := apiconv.NewMessage()
			upd.SetId(rec.ToolMessageID)
			upd.SetContent(errCopy)
			return upd
		}())
	}
	_ = s.conv.PatchToolCall(ctx, updTC)
	_ = s.conv.PatchMessage(ctx, func() *apiconv.MutableMessage {
		upd := apiconv.NewMessage()
		upd.SetId(rec.ToolMessageID)
		upd.SetStatus(status)
		return upd
	}())
	eventType := streaming.EventTypeToolCallCompleted
	if status == "failed" {
		eventType = streaming.EventTypeToolCallFailed
	}
	s.publishNestedToolLifecycleEvent(ctx, rec, toolName, eventType, status, strings.TrimSpace(responseBody), strings.TrimSpace(errMsg), nil)
}

func (s *Service) attachNestedRequestPayload(ctx context.Context, toolMsgID string, args map[string]interface{}) error {
	body, err := json.Marshal(args)
	if err != nil {
		return err
	}
	reqID, err := s.createInlinePayload(ctx, "tool_request", "application/json", body)
	if err != nil {
		return err
	}
	upd := apiconv.NewToolCall()
	upd.SetMessageID(toolMsgID)
	upd.RequestPayloadID = &reqID
	upd.Has.RequestPayloadID = true
	return s.conv.PatchToolCall(ctx, upd)
}

func (s *Service) createInlinePayload(ctx context.Context, kind, mime string, body []byte) (string, error) {
	if s == nil || s.conv == nil {
		return "", fmt.Errorf("conversation client not configured")
	}
	pid := uuid.NewString()
	p := apiconv.NewPayload()
	p.SetId(pid)
	p.SetKind(kind)
	p.SetMimeType(mime)
	p.SetSizeBytes(len(body))
	p.SetStorage("inline")
	p.SetInlineBody(body)
	if err := s.conv.PatchPayload(ctx, p); err != nil {
		return "", err
	}
	return pid, nil
}

func (s *Service) publishNestedToolLifecycleEvent(ctx context.Context, rec *nestedToolCallRecord, toolName string, eventType streaming.EventType, status, content, errMsg string, args map[string]interface{}) {
	if s == nil || s.streamPub == nil || rec == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	event := &streaming.Event{
		Type:               eventType,
		ConversationID:     strings.TrimSpace(turn.ConversationID),
		StreamID:           strings.TrimSpace(turn.ConversationID),
		TurnID:             strings.TrimSpace(turn.TurnID),
		MessageID:          strings.TrimSpace(rec.ToolMessageID),
		ToolMessageID:      strings.TrimSpace(rec.ToolMessageID),
		ToolCallID:         strings.TrimSpace(rec.ToolCallID),
		ToolName:           strings.TrimSpace(toolName),
		AssistantMessageID: assistantMessageID,
		ParentMessageID:    strings.TrimSpace(rec.ParentMessage),
		Status:             strings.TrimSpace(status),
		Content:            strings.TrimSpace(content),
		Error:              strings.TrimSpace(errMsg),
		Arguments:          args,
		CreatedAt:          time.Now(),
	}
	event.NormalizeIdentity(strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	_ = s.streamPub.Publish(ctx, event)
}

func (s *Service) emitNestedLinkedConversation(ctx context.Context, rec *nestedToolCallRecord, childConversationID, childAgentID, childTitle string) {
	if s == nil || s.streamPub == nil || rec == nil {
		return
	}
	turn, _ := runtimerequestctx.TurnMetaFromContext(ctx)
	assistantMessageID := strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	if assistantMessageID == "" {
		assistantMessageID = strings.TrimSpace(turn.ParentMessageID)
	}
	event := &streaming.Event{
		Type:                      streaming.EventTypeLinkedConversationAttached,
		ConversationID:            strings.TrimSpace(turn.ConversationID),
		StreamID:                  strings.TrimSpace(turn.ConversationID),
		TurnID:                    strings.TrimSpace(turn.TurnID),
		MessageID:                 strings.TrimSpace(rec.ToolMessageID),
		ToolMessageID:             strings.TrimSpace(rec.ToolMessageID),
		ToolCallID:                strings.TrimSpace(rec.ToolCallID),
		AssistantMessageID:        assistantMessageID,
		ParentMessageID:           strings.TrimSpace(rec.ParentMessage),
		LinkedConversationID:      strings.TrimSpace(childConversationID),
		LinkedConversationAgentID: strings.TrimSpace(childAgentID),
		LinkedConversationTitle:   strings.TrimSpace(childTitle),
		CreatedAt:                 time.Now(),
	}
	event.NormalizeIdentity(strings.TrimSpace(turn.ConversationID), strings.TrimSpace(turn.TurnID))
	_ = s.streamPub.Publish(ctx, event)
}

func (s *Service) publishSkillLifecycle(ctx context.Context, conversationID, skillName, args, executionID string, eventType streaming.EventType, status string, arguments map[string]interface{}) {
	if s == nil || s.streamPub == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(skillName) == "" {
		return
	}
	turnID := ""
	messageID := ""
	assistantMessageID := ""
	toolMessageID := ""
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		turnID = strings.TrimSpace(tm.TurnID)
		messageID = strings.TrimSpace(tm.ParentMessageID)
	}
	assistantMessageID = strings.TrimSpace(runtimerequestctx.ModelMessageIDFromContext(ctx))
	toolMessageID = strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx))
	if messageID == "" {
		messageID = assistantMessageID
	}
	if messageID == "" {
		messageID = toolMessageID
	}
	ev := &streaming.Event{
		StreamID:           strings.TrimSpace(conversationID),
		ConversationID:     strings.TrimSpace(conversationID),
		TurnID:             turnID,
		MessageID:          messageID,
		AssistantMessageID: assistantMessageID,
		ToolMessageID:      toolMessageID,
		ToolName:           "llm/skills:activate",
		Type:               eventType,
		SkillName:          strings.TrimSpace(skillName),
		SkillExecutionID:   strings.TrimSpace(executionID),
		Content:            strings.TrimSpace(args),
		Status:             strings.TrimSpace(status),
		Arguments:          arguments,
		CreatedAt:          time.Now(),
	}
	ev.NormalizeIdentity(strings.TrimSpace(conversationID), turnID)
	_ = s.streamPub.Publish(ctx, ev)
}

// publishRegistryUpdated emits a registry-updated event with no diff payload.
// Retained for back-compat callers; new code uses publishRegistryUpdatedWithDiff
// which carries {added, changed, removed} arrays alongside count and diagnostics.
func (s *Service) publishRegistryUpdated(ctx context.Context) {
	s.publishRegistryUpdatedWithDiff(ctx, nil, nil, nil)
}

// publishRegistryUpdatedWithDiff emits the existing
// EventTypeSkillRegistryUpdated event with the diff payload extension —
// added/changed/removed skill names by registry version. Same EventType
// constant, same publisher call site, richer Patch shape.
//
// The Replace decision (no parallel new event) means subscribers continue
// to receive a single event per registry change; the new arrays sit
// alongside the existing count/diagnostics fields. Subscribers that ignore
// the new keys keep working unchanged.
func (s *Service) publishRegistryUpdatedWithDiff(ctx context.Context, added, changed, removed []string) {
	if s == nil || s.streamPub == nil {
		return
	}
	count := 0
	diagnostics := 0
	s.mu.RLock()
	if s.registry != nil {
		count = len(s.registry.List())
		diagnostics = len(s.registry.Diagnostics())
	}
	s.mu.RUnlock()
	patch := map[string]interface{}{
		"count":       count,
		"diagnostics": diagnostics,
	}
	if len(added) > 0 {
		patch["added"] = added
	}
	if len(changed) > 0 {
		patch["changed"] = changed
	}
	if len(removed) > 0 {
		patch["removed"] = removed
	}
	ev := &streaming.Event{
		StreamID:       "skills",
		ConversationID: "skills",
		Type:           streaming.EventTypeSkillRegistryUpdated,
		Status:         "updated",
		Patch:          patch,
		CreatedAt:      time.Now(),
	}
	ev.NormalizeIdentity("skills", "")
	_ = s.streamPub.Publish(ctx, ev)
}

// skillFingerprints returns a snapshot map of skill name → fingerprint for
// the currently-loaded registry. Used by Load to compute a diff between
// pre/post-reload state. Fingerprint is a cheap content-derived string —
// frontmatter description + body length — sufficient to detect "changed"
// without computing a full hash on every reload.
func (s *Service) skillFingerprints() map[string]string {
	out := map[string]string{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.registry == nil {
		return out
	}
	for _, sk := range s.registry.List() {
		if sk == nil {
			continue
		}
		name := strings.TrimSpace(sk.Frontmatter.Name)
		if name == "" {
			continue
		}
		out[name] = sk.Frontmatter.Description + "|" + sk.Path + "|" + strconv.Itoa(len(sk.Body))
	}
	return out
}

// diffRegistries returns sorted name lists of additions, changes (same name,
// different fingerprint), and removals between two registry snapshots.
func diffRegistries(prior, current map[string]string) (added, changed, removed []string) {
	for name, fp := range current {
		if priorFP, ok := prior[name]; !ok {
			added = append(added, name)
		} else if priorFP != fp {
			changed = append(changed, name)
		}
	}
	for name := range prior {
		if _, ok := current[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(removed)
	return added, changed, removed
}

func (s *Service) Diagnostics() []string {
	if s == nil || s.registry == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for _, item := range s.registry.Diagnostics() {
		if msg := strings.TrimSpace(item.Message); msg != "" {
			out = append(out, msg)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Service) ListAll() []skillproto.Metadata {
	if s == nil || s.registry == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []skillproto.Metadata
	for _, item := range s.registry.List() {
		if item == nil {
			continue
		}
		out = append(out, skillproto.Metadata{
			Name:        item.Frontmatter.Name,
			Description: item.Frontmatter.Description,
		})
	}
	return out
}

func (s *Service) list(ctx context.Context, in, out interface{}) error {
	_, ok := in.(*ListInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	lo, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return err
	}
	lo.Items, _ = s.Visible(agent)
	lo.Diagnostics = s.Diagnostics()
	return nil
}

func (s *Service) activate(ctx context.Context, in, out interface{}) error {
	ai, ok := in.(*ActivateInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	ao, ok := out.(*ActivateOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	if convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx)); convID != "" {
		if override := requestedActivationModeOverride(ctx, ai); override != "" {
			ctx = WithActivationModeOverride(ctx, ai.Name, override)
		}
		agent, err := s.currentAgent(ctx)
		if err != nil {
			return err
		}
		body, mode, arguments, err := s.activateForConversationDetailed(ctx, convID, agent, ai.Name, ai.Args)
		if err != nil {
			return err
		}
		populateActivateOutput(ao, ai.Name, body, mode, arguments)
		return nil
	}
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return err
	}
	if override := requestedActivationModeOverride(ctx, ai); override != "" {
		ctx = WithActivationModeOverride(ctx, ai.Name, override)
	}
	body, _, err := s.activateWithContext(ctx, agent, ai.Name, ai.Args)
	if err != nil {
		return err
	}
	mode := "inline"
	if override := strings.TrimSpace(ai.Mode); override != "" {
		mode = skillproto.NormalizeContextMode(override)
	}
	populateActivateOutput(ao, ai.Name, body, mode, nil)
	return nil
}

func requestedActivationModeOverride(ctx context.Context, ai *ActivateInput) string {
	if ai == nil {
		return ""
	}
	override := strings.TrimSpace(ai.Mode)
	if override == "" {
		return ""
	}
	// The skill's declared execution mode is runtime-owned contract for
	// model-emitted tool calls. Owner LLMs may choose whether to activate a
	// skill, but they should not silently rewrite fork/detach skills back to
	// inline by passing input.mode. Runtime/API callers can still override the
	// mode explicitly when they invoke the tool outside the model tool path.
	if strings.TrimSpace(runtimerequestctx.ToolMessageIDFromContext(ctx)) != "" {
		return ""
	}
	return override
}

func populateActivateOutput(out *ActivateOutput, name, body, mode string, arguments map[string]interface{}) {
	if out == nil {
		return
	}
	out.Name = strings.TrimSpace(name)
	out.Body = strings.TrimSpace(body)
	out.Mode = strings.TrimSpace(mode)
	if strings.EqualFold(out.Mode, "inline") || strings.EqualFold(out.Mode, "fork") {
		out.Terminal = true
		out.Status = "completed"
	}
	if len(arguments) == 0 {
		return
	}
	if v, ok := arguments["childConversationId"].(string); ok {
		out.ChildConversationID = strings.TrimSpace(v)
		out.Started = out.ChildConversationID != ""
	}
	if v, ok := arguments["childAgentId"].(string); ok {
		out.ChildAgentID = strings.TrimSpace(v)
	}
	if v, ok := arguments["childStatus"].(string); ok && strings.TrimSpace(v) != "" {
		out.Status = strings.TrimSpace(v)
	}
	if v, ok := arguments["childTerminal"].(bool); ok {
		out.Terminal = v
	}
}

func (s *Service) currentAgent(ctx context.Context) (*agentmdl.Agent, error) {
	if s.agentFinder == nil || s.conv == nil {
		return nil, fmt.Errorf("skills service not configured")
	}
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if convID == "" {
		return nil, fmt.Errorf("missing conversation context")
	}
	conv, err := s.conv.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.AgentId == nil {
		return nil, fmt.Errorf("conversation agent not found")
	}
	return s.agentFinder.Find(ctx, strings.TrimSpace(*conv.AgentId))
}

func (s *Service) ListForConversation(ctx context.Context, conversationID string) ([]skillproto.Metadata, []string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return nil, nil, fmt.Errorf("conversation ID is required")
	}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return nil, nil, err
	}
	meta, _ := s.Visible(agent)
	return meta, s.Diagnostics(), nil
}

func (s *Service) ActivateForConversation(ctx context.Context, conversationID, name, args string) (string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", fmt.Errorf("conversation ID is required")
	}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return "", err
	}
	body, _, _, err := s.activateForConversationDetailed(ctx, conversationID, agent, name, args)
	return body, err
}

func (s *Service) ActivateForConversationWithAgent(ctx context.Context, conversationID string, agent *agentmdl.Agent, name, args string) (string, error) {
	body, _, _, err := s.activateForConversationDetailed(ctx, conversationID, agent, name, args)
	return body, err
}

func (s *Service) activateForConversationDetailed(ctx context.Context, conversationID string, agent *agentmdl.Agent, name, args string) (string, string, map[string]interface{}, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", "", nil, fmt.Errorf("conversation ID is required")
	}
	if agent == nil {
		return "", "", nil, fmt.Errorf("agent is required")
	}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	item, err := s.findVisibleSkill(agent, name)
	if err != nil {
		return "", "", nil, err
	}
	mode := effectiveActivationMode(ctx, item)
	executionID := uuid.NewString()
	startArgs := map[string]interface{}{"executionMode": mode}
	s.publishSkillLifecycle(ctx, conversationID, name, args, executionID, streaming.EventTypeSkillStarted, "running", startArgs)
	var (
		body      string
		stats     preprocessStats
		arguments map[string]interface{}
	)
	switch mode {
	case "fork", "detach":
		body, arguments, err = s.activateChildConversation(ctx, agent, item, args, mode)
	default:
		body, stats, err = s.activateResolvedWithContext(ctx, item, args)
	}
	if err == nil {
		if stats.CommandsRun > 0 || stats.Denied > 0 || stats.TimedOut > 0 || stats.BytesExpanded > 0 {
			if arguments == nil {
				arguments = map[string]interface{}{}
			}
			arguments["executionMode"] = mode
			arguments["preprocess"] = map[string]interface{}{
				"commandsRun":   stats.CommandsRun,
				"denied":        stats.Denied,
				"timedOut":      stats.TimedOut,
				"bytesExpanded": stats.BytesExpanded,
			}
		} else if arguments == nil {
			arguments = map[string]interface{}{"executionMode": mode}
		}
		s.publishSkillLifecycle(ctx, conversationID, name, args, executionID, streaming.EventTypeSkillCompleted, "completed", arguments)
	}
	return body, mode, arguments, err
}

func (s *Service) ActivateByPathForConversation(ctx context.Context, conversationID, skillPath, args string) (string, error) {
	if strings.TrimSpace(conversationID) == "" {
		return "", fmt.Errorf("conversation ID is required")
	}
	ctx = runtimerequestctx.WithConversationID(ctx, conversationID)
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return "", err
	}
	target := normalizeSkillPath(skillPath)
	for _, item := range s.visibleSkills(agent) {
		if item == nil {
			continue
		}
		if normalizeSkillPath(item.Path) == target {
			executionID := uuid.NewString()
			s.publishSkillLifecycle(ctx, conversationID, item.Frontmatter.Name, args, executionID, streaming.EventTypeSkillStarted, "running", nil)
			body, stats, err := s.activateWithContext(ctx, agent, item.Frontmatter.Name, args)
			if err == nil {
				var arguments map[string]interface{}
				if stats.CommandsRun > 0 || stats.Denied > 0 || stats.TimedOut > 0 || stats.BytesExpanded > 0 {
					arguments = map[string]interface{}{
						"preprocess": map[string]interface{}{
							"commandsRun":   stats.CommandsRun,
							"denied":        stats.Denied,
							"timedOut":      stats.TimedOut,
							"bytesExpanded": stats.BytesExpanded,
						},
					}
				}
				s.publishSkillLifecycle(ctx, conversationID, item.Frontmatter.Name, args, executionID, streaming.EventTypeSkillCompleted, "completed", arguments)
			}
			return body, err
		}
	}
	return "", fmt.Errorf("skill not available for path: %s", skillPath)
}

func ActiveSkillsFromHistory(history *binding.History) []string {
	if history == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	collect := func(turn *binding.Turn) {
		if turn == nil {
			return
		}
		for _, msg := range turn.Messages {
			if msg == nil || msg.Kind != binding.MessageKindToolResult {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm/skills:activate") &&
				!strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm/skills/activate") &&
				!strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm/skills-activate") &&
				!strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm_skills-activate") &&
				!strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm_skills/activate") &&
				!strings.EqualFold(strings.TrimSpace(msg.ToolName), "llm_skills:activate") {
				if strings.EqualFold(strings.TrimSpace(msg.ToolName), "resources:read") ||
					strings.EqualFold(strings.TrimSpace(msg.ToolName), "resources/read") ||
					strings.EqualFold(strings.TrimSpace(msg.ToolName), "resources-read") {
					var payload struct {
						SkillName string `json:"skillName"`
					}
					if err := json.Unmarshal([]byte(strings.TrimSpace(msg.Content)), &payload); err == nil {
						name := strings.TrimSpace(payload.SkillName)
						if name != "" {
							if _, ok := seen[name]; ok {
								continue
							}
							seen[name] = struct{}{}
							out = append(out, name)
						}
					}
				}
				continue
			}
			name, _ := msg.ToolArgs["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	collect(history.Current)
	return out
}

func InlineActiveSkillsFromHistory(history *binding.History, svc *Service, agent *agentmdl.Agent, overrideName, overrideMode string) []string {
	names := ActiveSkillsFromHistory(history)
	if len(names) == 0 || svc == nil || agent == nil {
		return names
	}
	inline := map[string]struct{}{}
	overrideName = strings.ToLower(strings.TrimSpace(overrideName))
	overrideMode = skillproto.NormalizeContextMode(overrideMode)
	for _, item := range svc.VisibleSkillsByName(agent, names) {
		if item == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(item.Frontmatter.Name))
		if name == "" {
			continue
		}
		mode := item.Frontmatter.ContextMode()
		if overrideName != "" && overrideMode == "inline" && overrideName == name {
			mode = "inline"
		}
		if mode != "inline" {
			continue
		}
		inline[name] = struct{}{}
	}
	if len(inline) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := inline[strings.ToLower(strings.TrimSpace(name))]; ok {
			out = append(out, name)
		}
	}
	return out
}

func normalizeSkillPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "file://") {
		if u, err := url.Parse(path); err == nil {
			path = u.Path
		}
	}
	return strings.TrimRight(path, "/")
}

// ExpandDefinitionsForActiveSkills returns the widened tool surface for the
// given active skills, ignoring any unmatched-pattern diagnostics. Existing
// callers that don't observe diagnostics use this.
func ExpandDefinitionsForActiveSkills(defs []*llm.ToolDefinition, reg tool.Registry, skills []*skillproto.Skill) []*llm.ToolDefinition {
	out, _ := ExpandDefinitionsForActiveSkillsWithDiag(defs, reg, skills)
	return out
}

// ExpandDefinitionsForActiveSkillsWithDiag returns the widened tool surface
// AND a list of allowed-tools patterns from the active skills that did not
// resolve to any registered tool. Callers can convert the unmatched list
// into warn-level skillproto.Diagnostics surfaced on the activation event
// so the model and operators see "skill 'X' requested tool 'Y' which is
// not available in the current agent's tool registry."
//
// Returns an empty unmatched slice when nothing is missing.
func ExpandDefinitionsForActiveSkillsWithDiag(defs []*llm.ToolDefinition, reg tool.Registry, skills []*skillproto.Skill) ([]*llm.ToolDefinition, []string) {
	return ExpandDefinitionsForConstraintsWithDiag(defs, reg, BuildConstraints(skills))
}
