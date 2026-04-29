package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	execconfig "github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
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
	s.mu.Lock()
	s.registry = reg
	s.mu.Unlock()
	s.publishRegistryUpdated(ctx)
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
		{Name: "activate", Description: "Activate one skill and return its SKILL.md body for the current turn", Input: reflect.TypeOf(&ActivateInput{}), Output: reflect.TypeOf(&ActivateOutput{})},
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
}
type ActivateOutput struct {
	Name string `json:"name,omitempty"`
	Body string `json:"body,omitempty"`
}

func (s *Service) Visible(agent *agentmdl.Agent) ([]skillproto.Metadata, string) {
	skills := s.visibleSkills(agent)
	meta := make([]skillproto.Metadata, 0, len(skills))
	for _, item := range skills {
		meta = append(meta, skillproto.Metadata{Name: item.Frontmatter.Name, Description: item.Frontmatter.Description})
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
	if item.Frontmatter.Preprocess {
		var diags []string
		body, diags, stats = preprocessBody(ctx, body, item, strings.TrimSpace(args), preprocessConversationID(ctx))
		if len(diags) > 0 {
			body = body + "\n\nDiagnostics:\n- " + strings.Join(diags, "\n- ")
		}
	}
	text := fmt.Sprintf("Loaded skill %q. Follow the instructions below:\n\n%s", strings.TrimSpace(item.Frontmatter.Name), body)
	if v := strings.TrimSpace(args); v != "" {
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
	if v := strings.TrimSpace(args); v != "" {
		return command + " " + v
	}
	return command
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

func (s *Service) activateChildConversation(ctx context.Context, agent *agentmdl.Agent, item *skillproto.Skill, args string) (string, map[string]interface{}, error) {
	if ExecFn == nil {
		return "", nil, fmt.Errorf("skill runtime not configured")
	}
	if agent == nil || strings.TrimSpace(agent.ID) == "" {
		return "", nil, fmt.Errorf("skill %q requires an agent identity for %s execution", strings.TrimSpace(item.Frontmatter.Name), item.Frontmatter.ContextMode())
	}
	mode := item.Frontmatter.ContextMode()
	startPayload := map[string]interface{}{
		"agentId":       strings.TrimSpace(agent.ID),
		"objective":     explicitSkillObjective(item.Frontmatter.Name, args),
		"executionMode": mode,
		"context": map[string]interface{}{
			"skillActivationName": strings.TrimSpace(item.Frontmatter.Name),
			"skillActivationMode": "inline",
		},
	}
	serialized, err := ExecFn(ctx, "llm/agents:start", startPayload)
	if err != nil {
		return "", nil, err
	}
	startOut, err := parseAgentsStartOutput(serialized)
	if err != nil {
		return "", nil, fmt.Errorf("parse llm/agents:start output: %w", err)
	}
	childConversationID := strings.TrimSpace(startOut.ConversationID)
	if childConversationID == "" {
		return "", nil, fmt.Errorf("llm/agents:start returned empty conversationId for skill %q", strings.TrimSpace(item.Frontmatter.Name))
	}
	eventArgs := map[string]interface{}{
		"executionMode":       mode,
		"childConversationId": childConversationID,
		"childStatus":         strings.TrimSpace(startOut.Status),
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

func (s *Service) publishRegistryUpdated(ctx context.Context) {
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
	ev := &streaming.Event{
		StreamID:       "skills",
		ConversationID: "skills",
		Type:           streaming.EventTypeSkillRegistryUpdated,
		Status:         "updated",
		Patch: map[string]interface{}{
			"count":       count,
			"diagnostics": diagnostics,
		},
		CreatedAt: time.Now(),
	}
	ev.NormalizeIdentity("skills", "")
	_ = s.streamPub.Publish(ctx, ev)
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
		body, err := s.ActivateForConversation(ctx, convID, ai.Name, ai.Args)
		if err != nil {
			return err
		}
		ao.Name = strings.TrimSpace(ai.Name)
		ao.Body = body
		return nil
	}
	agent, err := s.currentAgent(ctx)
	if err != nil {
		return err
	}
	body, err := s.Activate(agent, ai.Name, ai.Args)
	if err != nil {
		return err
	}
	ao.Name = strings.TrimSpace(ai.Name)
	ao.Body = body
	return nil
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
	item, err := s.findVisibleSkill(agent, name)
	if err != nil {
		return "", err
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
		body, arguments, err = s.activateChildConversation(ctx, agent, item, args)
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
	return body, err
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

func NarrowDefinitionsForActiveSkills(defs []*llm.ToolDefinition, skills []*skillproto.Skill) []*llm.ToolDefinition {
	return NarrowDefinitionsForConstraints(defs, BuildConstraints(skills))
}

func ExpandDefinitionsForActiveSkills(defs []*llm.ToolDefinition, reg tool.Registry, skills []*skillproto.Skill) []*llm.ToolDefinition {
	return ExpandDefinitionsForConstraints(defs, reg, BuildConstraints(skills))
}
