package prompt

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	promptdef "github.com/viant/agently-core/protocol/prompt"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/toolexec"
	promptrepo "github.com/viant/agently-core/workspace/repository/prompt"
)

const Name = "prompt"

// Service implements the prompt:list and prompt:get tools.
type Service struct {
	repo   *promptrepo.Repository
	conv   apiconv.Client
	finder agentmdl.Finder
	mgr    promptdef.MCPManager // optional; enables MCP-sourced profiles
}

func New(repo *promptrepo.Repository, opts ...func(*Service)) *Service {
	s := &Service{repo: repo}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func WithConversationClient(c apiconv.Client) func(*Service) { return func(s *Service) { s.conv = c } }
func WithAgentFinder(f agentmdl.Finder) func(*Service)       { return func(s *Service) { s.finder = f } }
func WithMCPManager(m promptdef.MCPManager) func(*Service)   { return func(s *Service) { s.mgr = m } }

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List available prompt profiles by id and description for scenario selection", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get a prompt profile by id, returning rendered messages and metadata. Use includeDocument:true to inject instructions into the current conversation.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
	}
}

func (s *Service) Method(name string) (svc.Executable, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list":
		return s.list, nil
	case "get":
		return s.get, nil
	default:
		return nil, svc.NewMethodNotFoundError(name)
	}
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
	profiles, err := s.allowedProfiles(ctx)
	if err != nil {
		return err
	}
	items := make([]ListItem, 0, len(profiles))
	for _, p := range profiles {
		if p == nil {
			continue
		}
		items = append(items, ListItem{
			ID:          strings.TrimSpace(p.ID),
			Name:        strings.TrimSpace(p.Name),
			Description: strings.TrimSpace(p.Description),
			AppliesTo:   p.AppliesTo,
			ToolBundles: p.ToolBundles,
			Template:    strings.TrimSpace(p.Template),
			Templates:   append([]string(nil), p.Templates...),
		})
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].ID) < strings.ToLower(items[j].ID) })
	lo.Profiles = items
	return nil
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	gi, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	go_, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	id := strings.TrimSpace(gi.ID)
	if id == "" {
		return fmt.Errorf("prompt profile id is required")
	}
	profiles, err := s.allowedProfiles(ctx)
	if err != nil {
		return err
	}
	var selected *promptdef.Profile
	for _, p := range profiles {
		if p == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(p.ID), id) {
			selected = p
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("prompt profile %q not found", id)
	}

	go_.ID = strings.TrimSpace(selected.ID)
	go_.Name = strings.TrimSpace(selected.Name)
	go_.Description = strings.TrimSpace(selected.Description)
	go_.ToolBundles = selected.ToolBundles
	go_.PreferredTools = selected.PreferredTools
	go_.Template = strings.TrimSpace(selected.Template)
	go_.Templates = append([]string(nil), selected.Templates...)
	go_.Resources = selected.Resources

	// Always render and return messages in the response body.
	// Render supports local text/URI messages and MCP-sourced messages.
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	renderOpts := &promptdef.RenderOptions{ConversationID: convID}
	msgs, err := selected.Render(ctx, s.mgr, renderOpts)
	if err != nil {
		return fmt.Errorf("render profile %q: %w", id, err)
	}
	rendered := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		rendered = append(rendered, Message{Role: m.Role, Text: m.Text})
	}
	go_.Messages = rendered

	// Optionally inject into the conversation.
	if gi.IncludeDocument != nil && *gi.IncludeDocument {
		if err := s.injectMessages(ctx, msgs); err != nil {
			return err
		}
		go_.Injected = true
	}
	return nil
}

// injectMessages writes each message into the current conversation with its
// authored role.  System messages are stored as system documents
// (SystemDocumentMode + SystemDocumentTag); user and assistant messages are
// stored with their natural roles only.
func (s *Service) injectMessages(ctx context.Context, msgs []promptdef.Message) error {
	if s == nil || s.conv == nil || len(msgs) == 0 {
		return nil
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return nil
	}
	for i, msg := range msgs {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		opts := []apiconv.MessageOption{
			apiconv.WithRole(role),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("prompt"),
			apiconv.WithContent(text),
			apiconv.WithContextSummary(fmt.Sprintf("prompt://message/%d", i)),
			apiconv.WithCreatedAt(time.Now()),
		}
		if role == "system" {
			opts = append(opts,
				apiconv.WithMode(toolexec.SystemDocumentMode),
				apiconv.WithTags(toolexec.SystemDocumentTag),
			)
		}
		if _, err := apiconv.AddMessage(ctx, s.conv, &turn, opts...); err != nil {
			return err
		}
	}
	return nil
}

// allowedProfiles returns all profiles visible to the current agent.
// When the agent has no Prompts.Bundles restriction, all profiles are returned.
func (s *Service) allowedProfiles(ctx context.Context) ([]*promptdef.Profile, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("prompt repository not configured")
	}
	all, err := s.repo.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	// No agent-scoped filtering configured — return everything.
	agentID := s.currentAgentID(ctx)
	if agentID == "" || s.finder == nil {
		return all, nil
	}
	ag, err := s.finder.Find(ctx, agentID)
	if err != nil || ag == nil || len(ag.Prompts.Bundles) == 0 {
		return all, nil
	}
	allowed := make(map[string]struct{}, len(ag.Prompts.Bundles))
	for _, b := range ag.Prompts.Bundles {
		allowed[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
	}
	filtered := make([]*promptdef.Profile, 0, len(all))
	for _, p := range all {
		if p == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(p.ID))]; ok {
			filtered = append(filtered, p)
		}
	}
	return filtered, nil
}

func (s *Service) currentAgentID(ctx context.Context) string {
	if s == nil || s.conv == nil {
		return ""
	}
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if convID == "" {
		return ""
	}
	conv, err := s.conv.GetConversation(ctx, convID)
	if err != nil || conv == nil || conv.AgentId == nil {
		return ""
	}
	return strings.TrimSpace(*conv.AgentId)
}
