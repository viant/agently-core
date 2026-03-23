package agents

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	svc "github.com/viant/agently-core/protocol/tool/service"
	"github.com/viant/agently-core/runtime/memory"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	linksvc "github.com/viant/agently-core/service/linking"
	executil "github.com/viant/agently-core/service/shared/executil"
	statussvc "github.com/viant/agently-core/service/toolstatus"
)

const Name = "llm/agents"
const defaultMaxSameAgentDepth = 2

// agentRuntime abstracts the subset of the agent service used by this
// tool, allowing unit tests to inject a lightweight fake.
type agentRuntime interface {
	Query(ctx context.Context, input *agentsvc.QueryInput, output *agentsvc.QueryOutput) error
	Finder() agentmdl.Finder
}

// Service exposes agent directory and execution as tool methods.
type Service struct {
	agent       agentRuntime
	dirProvider func() []ListItem
	// Optional external runner: returns answer, status, taskID, contextID, streamSupported, warnings
	runExternal func(ctx context.Context, agentID, objective string, payload map[string]interface{}) (string, string, string, string, bool, []string, error)
	// Routing policy
	strict  bool
	allowed map[string]string // id -> source (internal|external)
	// Conversation/linking/status helpers
	conv   apiconv.Client
	linker *linksvc.Service
	status *statussvc.Service
	// ChildTimeout overrides DefaultChildAgentTimeout for internal runs.
	// Zero means use DefaultChildAgentTimeout.
	ChildTimeout time.Duration
}

// New creates a Service bound to the internal agent runtime.
type Option func(*Service)

func WithDirectoryProvider(f func() []ListItem) Option {
	return func(s *Service) { s.dirProvider = f }
}

// WithExternalRunner configures an external execution path resolver used when
// the agentId refers to an external A2A entry.
func WithExternalRunner(run func(ctx context.Context, agentID, objective string, payload map[string]interface{}) (answer, status, taskID, contextID string, streamSupported bool, warnings []string, err error)) Option {
	return func(s *Service) { s.runExternal = run }
}

// WithStrict enables strict directory routing: only ids present in the directory may be run.
func WithStrict(v bool) Option { return func(s *Service) { s.strict = v } }

// WithAllowedIDs configures the set of allowed agent ids (directory view).
func WithAllowedIDs(ids map[string]string) Option { return func(s *Service) { s.allowed = ids } }

// WithConversationClient injects the conversation client and initializes linking/status helpers.
func WithConversationClient(c apiconv.Client) Option {
	return func(s *Service) {
		s.conv = c
		if c != nil {
			s.linker = linksvc.New(c)
			s.status = statussvc.New(c)
		}
	}
}

// WithStreamPublisher wires a streaming publisher to the linking service so
// linked_conversation_attached events reach the SSE bus.
func WithStreamPublisher(p streaming.Publisher) Option {
	return func(s *Service) {
		if s.linker != nil && p != nil {
			s.linker.SetStreamPublisher(p)
		}
	}
}

func New(agent *agentsvc.Service, opts ...Option) *Service {
	s := &Service{agent: agent}
	for _, o := range opts {
		if o != nil {
			o(s)
		}
	}
	return s
}

// Name returns the service name.
func (s *Service) Name() string { return Name }

// ToolTimeout suggests a larger timeout for llm/agents service tools which run
// full agent turns.
func (s *Service) ToolTimeout() time.Duration { return 15 * time.Minute }

// Methods returns available methods.
func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{
			Name:        "list",
			Description: "List available agents for selection (filtered directory)",
			Input:       reflect.TypeOf(&struct{}{}),
			Output:      reflect.TypeOf(&ListOutput{}),
		},
		{
			Name:        "me",
			Description: "Return conversation id, agent name, and model used for the current context",
			Input:       reflect.TypeOf(&struct{}{}),
			Output:      reflect.TypeOf(&MeOutput{}),
		},
		{
			Name:        "status",
			Description: "Return linked child conversation statuses and latest assistant output for a child or parent conversation",
			Input:       reflect.TypeOf(&StatusInput{}),
			Output:      reflect.TypeOf(&StatusOutput{}),
		},
		{
			Name:        "run",
			Description: "Run an agent by id with an objective and optional context",
			Input:       reflect.TypeOf(&RunInput{}),
			Output:      reflect.TypeOf(&RunOutput{}),
		},
	}
}

// Method resolves a method by name.
func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(name) {
	case "list":
		return s.list, nil
	case "me":
		return s.me, nil
	case "status":
		return s.statusMethod, nil
	case "run":
		return s.run, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
}

// list returns an empty directory for now. It will be populated in later phases
// with configured internal and external agent entries.
func (s *Service) list(ctx context.Context, in, out interface{}) error {
	// Accept either nil or empty struct as input
	lo, ok := out.(*ListOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	lo.ReuseNote = "Reuse this directory for the rest of the current turn. Do not call llm/agents:list again unless the available agents changed."
	lo.RunUsage = "To delegate next, call llm/agents:run with {agentId, objective} and include context.workdir when repo scope matters."
	lo.NextAction = "If you already know the target agent from items or the injected agent directory document, skip llm/agents:list and call llm/agents:run directly."
	if s.dirProvider != nil {
		lo.Items = s.dirProvider()
		return nil
	}
	lo.Items = nil
	return nil
}

// me returns the current conversation id, agent name, and model used (best-effort).
func (s *Service) me(ctx context.Context, in, out interface{}) error {
	mo, ok := out.(*MeOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	mo.ConversationID = strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	// Best-effort: load conversation to get agent id + model
	if s.conv != nil && mo.ConversationID != "" {
		if c, err := s.conv.GetConversation(ctx, mo.ConversationID); err == nil && c != nil {
			if c.AgentId != nil && strings.TrimSpace(*c.AgentId) != "" {
				if s.agent != nil && s.agent.Finder() != nil {
					if ag, err := s.agent.Finder().Find(ctx, strings.TrimSpace(*c.AgentId)); err == nil && ag != nil && ag.Profile != nil {
						mo.AgentName = strings.TrimSpace(ag.Profile.Name)
					}
				}
				if mo.AgentName == "" {
					mo.AgentName = strings.TrimSpace(*c.AgentId)
				}
			}
			if c.DefaultModel != nil && strings.TrimSpace(*c.DefaultModel) != "" {
				mo.Model = strings.TrimSpace(*c.DefaultModel)
			}
		}
	}
	return nil
}

// run executes an internal agent synchronously via the agent runtime.
// External routing and streaming/status publishing will be added in later phases.
func (s *Service) run(ctx context.Context, in, out interface{}) error {
	ri, ok := in.(*RunInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	ro, ok := out.(*RunOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	maxDepth := s.maxDelegationDepth(ctx, strings.TrimSpace(ri.AgentID))
	depth := delegationDepthFor(ri.Context, strings.TrimSpace(ri.AgentID))
	if depth >= maxDepth {
		ro.Status = "skipped"
		ro.Answer = "delegation depth reached for agent " + strings.TrimSpace(ri.AgentID)
		return nil
	}
	convID := strings.TrimSpace(memory.ConversationIDFromContext(ctx))
	if convID == "" {
		if v := strings.TrimSpace(ri.ConversationID); v != "" {
			convID = v
			ctx = memory.WithConversationID(ctx, convID)
		}
	}
	ro.ConversationID = convID
	debugf("agents.run start convo=%q agent_id=%q objective_len=%d objective_head=%q objective_tail=%q context_keys=%d", strings.TrimSpace(convID), strings.TrimSpace(ri.AgentID), len(ri.Objective), headString(ri.Objective, 512), tailString(ri.Objective, 512), len(ri.Context))
	// Strict routing: require id present in directory
	if s.strict {
		if _, ok := s.allowed[strings.TrimSpace(ri.AgentID)]; !ok {
			errorf("agents.run strict reject agent_id=%q", strings.TrimSpace(ri.AgentID))
			return svc.NewMethodNotFoundError("agent not registered in directory: " + strings.TrimSpace(ri.AgentID))
		}
	}
	// Resolve intended route when directory provided
	intended := ""
	if s.allowed != nil {
		if v, ok := s.allowed[strings.TrimSpace(ri.AgentID)]; ok {
			intended = v
		}
	}
	debugf("agents.run routing agent_id=%q intended=%q", strings.TrimSpace(ri.AgentID), strings.TrimSpace(intended))

	// Default to internal when the agent is resolvable locally; only fall back to
	// external when explicitly routed or when the agent id is not found internally.
	internalKnown := s.isInternalAgent(ctx, strings.TrimSpace(ri.AgentID))
	debugf("agents.run route check agent_id=%q internal_known=%v external_enabled=%v", strings.TrimSpace(ri.AgentID), internalKnown, s.runExternal != nil)
	if s.runExternal != nil && (intended == "external" || (intended == "" && !internalKnown)) {
		handled, err := s.tryExternalRun(ctx, ri, ro, intended)
		if handled || err != nil {
			return err
		}
		// If we reach here: route was unknown and external execution failed; fall back to internal.
	}
	return s.runInternal(ctx, ri, ro, convID, depth)
}

func (s *Service) waitForConversation(ctx context.Context, conversationID string) error {
	if s == nil || s.conv == nil || strings.TrimSpace(conversationID) == "" {
		return nil
	}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		conv, err := s.conv.GetConversation(ctx, strings.TrimSpace(conversationID))
		if err == nil && conv != nil {
			return nil
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = svc.NewMethodNotFoundError("conversation not found: " + strings.TrimSpace(conversationID))
		}
		if ctx.Err() != nil {
			break
		}
		delay := 100 * time.Millisecond << attempt
		if delay > time.Second {
			delay = time.Second
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return nil
}

func (s *Service) maxDelegationDepth(ctx context.Context, agentID string) int {
	if strings.TrimSpace(agentID) == "" {
		return defaultMaxSameAgentDepth
	}
	if s == nil || isNilAgentRuntime(s.agent) {
		return defaultMaxSameAgentDepth
	}
	finder := s.agent.Finder()
	if finder == nil {
		return defaultMaxSameAgentDepth
	}
	if ag, err := finder.Find(ctx, strings.TrimSpace(agentID)); err == nil && ag != nil && ag.Delegation != nil && ag.Delegation.MaxDepth > 0 {
		return ag.Delegation.MaxDepth
	}
	return defaultMaxSameAgentDepth
}

func isNilAgentRuntime(v agentRuntime) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

func delegationDepthFor(ctx map[string]interface{}, agentID string) int {
	if ctx == nil || strings.TrimSpace(agentID) == "" {
		return 0
	}
	raw, ok := ctx["DelegationDepths"]
	if !ok || raw == nil {
		return 0
	}
	switch m := raw.(type) {
	case map[string]interface{}:
		if v, ok := m[agentID]; ok {
			return asInt(v)
		}
	case map[string]int:
		return m[agentID]
	case map[string]float64:
		if v, ok := m[agentID]; ok {
			return int(v)
		}
	}
	return 0
}

func setDelegationDepth(ctx map[string]interface{}, agentID string, depth int) map[string]interface{} {
	if ctx == nil {
		ctx = map[string]interface{}{}
	}
	raw, ok := ctx["DelegationDepths"]
	var m map[string]interface{}
	if ok {
		if mm, ok := raw.(map[string]interface{}); ok {
			m = mm
		}
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	m[agentID] = depth
	ctx["DelegationDepths"] = m
	return ctx
}

func inheritDelegatedContext(ctx context.Context, child map[string]interface{}) map[string]interface{} {
	if child == nil {
		child = map[string]interface{}{}
	}
	if _, ok := child["workdir"]; !ok {
		if workdir, ok := executil.WorkdirFromContext(ctx); ok && strings.TrimSpace(workdir) != "" {
			child["workdir"] = strings.TrimSpace(workdir)
		}
	}
	if _, ok := child["resolvedWorkdir"]; !ok {
		if workdir, ok := child["workdir"].(string); ok && strings.TrimSpace(workdir) != "" {
			child["resolvedWorkdir"] = strings.TrimSpace(workdir)
		}
	}
	return child
}

func asInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return 0
}

func (s *Service) isInternalAgent(ctx context.Context, agentID string) bool {
	if s == nil || s.agent == nil || strings.TrimSpace(agentID) == "" {
		return false
	}
	// Handle typed-nil interfaces (e.g. var x *T=nil; interface{...}=x).
	if v := reflect.ValueOf(s.agent); v.Kind() == reflect.Pointer && v.IsNil() {
		return false
	}
	if s.agent.Finder() == nil {
		return false
	}
	ag, err := s.agent.Finder().Find(ctx, strings.TrimSpace(agentID))
	return err == nil && ag != nil
}

func attachLinkedConversation(ctx context.Context, conv apiconv.Client, parent memory.TurnMeta, statusMessageID, linkedConversationID string) {
	if conv == nil || strings.TrimSpace(linkedConversationID) == "" {
		return
	}
	messageIDs := []string{strings.TrimSpace(statusMessageID)}
	if toolMsgID := strings.TrimSpace(memory.ToolMessageIDFromContext(ctx)); toolMsgID != "" && toolMsgID != strings.TrimSpace(statusMessageID) {
		messageIDs = append(messageIDs, toolMsgID)
	}
	for _, messageID := range messageIDs {
		if messageID == "" {
			continue
		}
		patch := apiconv.NewMessage()
		patch.SetId(messageID)
		patch.SetConversationID(strings.TrimSpace(parent.ConversationID))
		patch.SetTurnID(strings.TrimSpace(parent.TurnID))
		patch.SetLinkedConversationID(strings.TrimSpace(linkedConversationID))
		if err := conv.PatchMessage(ctx, patch); err != nil {
			errorf("agents.run attach linked conversation error message_id=%q linked_convo=%q err=%v", messageID, strings.TrimSpace(linkedConversationID), err)
		}
	}
}

func (s *Service) lookupReusableChildConversation(ctx context.Context, in *agconv.ConversationInput) string {
	if s == nil || s.conv == nil || in == nil {
		return ""
	}
	items, err := s.conv.GetConversations(ctx, (*apiconv.Input)(in))
	if err != nil || len(items) == 0 {
		return ""
	}
	var picked *apiconv.Conversation
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.Id) == "" {
			continue
		}
		if picked == nil {
			picked = item
			continue
		}
		pickedTime := picked.CreatedAt
		if picked.UpdatedAt != nil && !picked.UpdatedAt.IsZero() {
			pickedTime = *picked.UpdatedAt
		}
		itemTime := item.CreatedAt
		if item.UpdatedAt != nil && !item.UpdatedAt.IsZero() {
			itemTime = *item.UpdatedAt
		}
		if itemTime.After(pickedTime) {
			picked = item
		}
	}
	if picked == nil {
		return ""
	}
	return strings.TrimSpace(picked.Id)
}
