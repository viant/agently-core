package prompt

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/primitive"
	"net/url"
	"reflect"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	intent "github.com/viant/agently-core/protocol/intent"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	policy "github.com/viant/agently-core/service/policy"
	"github.com/viant/agently-core/service/shared/toolexec"
	intakerepo "github.com/viant/agently-core/workspace/repository/intake"
)

const Name = "prompt"
const PlaybookName = "playbook"

// Service implements the prompt:list and prompt:get tools.
type Service struct {
	name   string
	repo   *intakerepo.Repository
	conv   apiconv.Client
	finder agentmdl.Finder
	mgr    intent.MCPManager // optional; enables MCP-sourced profiles
	policy *policy.Runtime
}

func New(repo *intakerepo.Repository, opts ...func(*Service)) *Service {
	s := &Service{name: Name, repo: repo}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// NewPlaybook exposes the same admitted handlers under the preferred tool
// namespace. prompt:list/get remain exact legacy aliases.
func NewPlaybook(repo *intakerepo.Repository, opts ...func(*Service)) *Service {
	s := New(repo, opts...)
	s.name = PlaybookName
	return s
}

func WithConversationClient(c apiconv.Client) func(*Service) { return func(s *Service) { s.conv = c } }
func WithAgentFinder(f agentmdl.Finder) func(*Service)       { return func(s *Service) { s.finder = f } }
func WithMCPManager(m intent.MCPManager) func(*Service) {
	return func(s *Service) {
		if m == nil || (reflect.ValueOf(m).Kind() == reflect.Pointer && reflect.ValueOf(m).IsNil()) {
			return
		}
		s.mgr = m
	}
}
func WithAuthorizationPolicy(p *policy.Runtime) func(*Service) {
	return func(s *Service) { s.policy = p }
}

func (s *Service) Name() string {
	if s.name == "" {
		return Name
	}
	return s.name
}

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "Find available playbook summaries using search (BM25-ranked natural-language search), namespace, nameTerms, descriptionTerms, keywords, or excludeKeywords. Literal groups use OR within each group and AND across groups. Returns seven summaries by default; use limit and nextCursor with unchanged filters. No instruction bodies are returned. After selecting a playbook to follow, call get with includeDocument:true.", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get a playbook by id. When selecting it to guide the current task, set includeDocument:true: this injects its authored system/user messages in order and activates its declared tools in the running agent. Tool declarations may use existing wildcard patterns. Search for more tools only when the playbook lacks a needed capability. Omit or set false only to inspect the playbook without activating it. Check injected in the result.", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
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
	input, ok := in.(*ListInput)
	if !ok || input == nil {
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
			URI:              p.ResourceURI,
			ID:               strings.TrimSpace(p.ID),
			Name:             strings.TrimSpace(p.Name),
			Description:      strings.TrimSpace(p.Description),
			AppliesTo:        p.AppliesTo,
			EvidenceContract: mapEvidenceContract(p.EvidenceContract),
			ToolBundles:      p.ToolBundles,
			Template:         strings.TrimSpace(p.Template),
			Templates:        append([]string(nil), p.Templates...),
			Knowledge:        append([]intent.KnowledgeMatch(nil), p.Knowledge...),
		})
	}
	return s.listPage(ctx, input, items, lo)
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
		return fmt.Errorf("intake profile id is required")
	}
	profiles, err := s.allowedProfiles(ctx)
	if err != nil {
		return err
	}
	var selected *intent.Profile
	for _, p := range profiles {
		if p == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(p.ID), id) || p.ResourceURI == id {
			if selected != nil {
				return fmt.Errorf("ambiguous authored definition: %s", id)
			}
			selected = p
		}
	}
	if selected == nil {
		return fmt.Errorf("intake profile %q not found", id)
	}

	fresh, err := s.repo.AdmitSelected(ctx, id, selected)
	if err != nil {
		return err
	}
	if fresh == nil {
		return fmt.Errorf("intent not found")
	}
	selected = fresh
	go_.ID = strings.TrimSpace(selected.ID)
	go_.Name = strings.TrimSpace(selected.Name)
	go_.Description = strings.TrimSpace(selected.Description)
	go_.EvidenceContract = mapEvidenceContract(selected.EvidenceContract)
	go_.ToolBundles = selected.ToolBundles
	go_.PreferredTools = selected.PreferredTools
	go_.Template = strings.TrimSpace(selected.Template)
	go_.Templates = append([]string(nil), selected.Templates...)
	go_.Resources = selected.Resources
	go_.Knowledge = append([]intent.KnowledgeMatch(nil), selected.Knowledge...)

	// Render the selected messages for inspection or role-preserving activation.
	// Render supports local text/URI messages and MCP-sourced messages.
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	renderOpts := &intent.RenderOptions{ConversationID: convID}
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
		knowledge, err := prepareKnowledgeActivation(ctx, selected)
		if err != nil {
			return err
		}
		activate, err := prepareToolActivation(ctx, selected)
		if err != nil {
			return err
		}
		ids, err := s.injectMessages(ctx, append(msgs, knowledge...))
		if err != nil {
			return err
		}
		if activate != nil {
			if err := activate(); err != nil {
				return err
			}
		}
		// Instructions are already role-preserving conversation messages. Return
		// only their receipt, not a second copy in the tool response.
		*go_ = GetOutput{ID: go_.ID, Injected: true, MessageIDs: ids}
	}
	return nil
}

// injectMessages writes each message into the current conversation with its
// authored role.  System messages are stored as system documents
// (SystemDocumentMode + SystemDocumentTag); user and assistant messages are
// stored with their natural roles only.
func (s *Service) injectMessages(ctx context.Context, msgs []intent.Message) ([]string, error) {
	if s == nil || s.conv == nil {
		return nil, fmt.Errorf("playbook activation requires conversation storage")
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return nil, fmt.Errorf("playbook activation requires a current conversation turn")
	}
	ids := make([]string, 0, len(msgs))
	for i, msg := range msgs {
		text := strings.TrimSpace(msg.Text)
		if text == "" {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		summary := strings.TrimSpace(msg.URI)
		if summary == "" {
			summary = fmt.Sprintf("prompt://message/%d", i)
		}
		opts := []apiconv.MessageOption{
			apiconv.WithRole(role),
			apiconv.WithType("text"),
			apiconv.WithCreatedByUserID("prompt"),
			apiconv.WithContent(text),
			apiconv.WithContextSummary(summary),
			apiconv.WithCreatedAt(time.Now()),
		}
		if role == "system" {
			opts = append(opts,
				apiconv.WithMode(toolexec.SystemDocumentMode),
				apiconv.WithTags(toolexec.SystemDocumentTag),
			)
		}
		message, err := apiconv.AddMessage(ctx, s.conv, &turn, opts...)
		if err != nil {
			return ids, err
		}
		ids = append(ids, message.Id)
	}
	return ids, nil
}

// allowedProfiles returns all profiles visible to the current agent.
// When the agent has no Prompts.Bundles restriction, all profiles are returned.
func (s *Service) allowedProfiles(ctx context.Context) ([]*intent.Profile, error) {
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
		return s.filterAuthorizedProfiles(ctx, all)
	}
	ag, err := s.finder.Find(ctx, agentID)
	if err != nil || ag == nil || len(ag.Prompts.Bundles) == 0 {
		return s.filterAuthorizedProfiles(ctx, all)
	}
	allowed := make(map[string]struct{}, len(ag.Prompts.Bundles))
	for _, b := range ag.Prompts.Bundles {
		allowed[strings.ToLower(strings.TrimSpace(b))] = struct{}{}
	}
	filtered := make([]*intent.Profile, 0, len(all))
	for _, p := range all {
		if p == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(p.ID))]; ok {
			filtered = append(filtered, p)
		}
	}
	return s.filterAuthorizedProfiles(ctx, filtered)
}

func (s *Service) filterAuthorizedProfiles(ctx context.Context, profiles []*intent.Profile) ([]*intent.Profile, error) {
	if s == nil || s.policy == nil || !s.policy.IsEnabled(policy.OperationIntentView) || len(profiles) == 0 {
		return profiles, nil
	}
	candidates := make([]policy.Candidate, 0, len(profiles))
	byID := make(map[string][]*intent.Profile, len(profiles))
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		id := strings.TrimSpace(profile.ID)
		if id == "" {
			continue
		}
		candidates = append(candidates, policy.Candidate{ID: id, Kind: "promptProfile"})
		byID[strings.ToLower(id)] = append(byID[strings.ToLower(id)], profile)
	}
	allowed, err := s.policy.Filter(ctx, policy.OperationIntentView,
		runtimerequestctx.ConversationIDFromContext(ctx), candidates,
		map[string]any{"agentId": s.currentAgentID(ctx)})
	if errors.Is(err, policy.ErrIdentityRejected) {
		return nil, err
	}
	if errors.Is(err, policy.ErrDenied) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := make([]*intent.Profile, 0, len(allowed))
	seenAllowed := map[string]bool{}
	for _, candidate := range allowed {
		key := strings.ToLower(strings.TrimSpace(candidate.ID))
		if seenAllowed[key] {
			continue
		}
		seenAllowed[key] = true
		if items := byID[key]; len(items) > 0 {
			result = append(result, items...)
		}
	}
	return result, nil
}

func (s *Service) currentAgentID(ctx context.Context) string {
	if tm, ok := runtimerequestctx.TurnMetaFromContext(ctx); ok {
		if agentID := strings.TrimSpace(tm.Assistant); agentID != "" {
			return agentID
		}
	}
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

func mapEvidenceContract(in *intent.EvidenceContract) *EvidenceContract {
	if in == nil {
		return nil
	}
	return &EvidenceContract{
		Required:   append([]string(nil), in.Required...),
		Optional:   append([]string(nil), in.Optional...),
		Forbidden:  append([]string(nil), in.Forbidden...),
		Completion: append([]string(nil), in.Completion...),
	}
}

// playbookCatalogOwner is the existing shared MCP metadata-cache boundary.
type playbookCatalogOwner interface {
	CatalogScope(context.Context, string, []string) (string, bool, error)
	VisibleCatalog(context.Context, string, time.Time, func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), ...func() time.Time) (*primitive.Catalog[json.RawMessage], error)
	CheckVisibleCatalog(context.Context, *primitive.Catalog[json.RawMessage]) error
}

func (s *Service) listPage(ctx context.Context, input *ListInput, items []ListItem, out *ListOutput) error {
	query := input.CatalogQuery
	if query.Limit <= 0 {
		query.Limit = 7
	}
	// Admission and agent presentation filtering have already run. The exact
	// summary digest makes source or eligibility changes invalidate old cursors.
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		entries := make([]primitive.CatalogItem[json.RawMessage], 0, len(items))
		for _, item := range items {
			value, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			namespace := "workspace"
			if u, e := url.Parse(item.URI); e == nil && u.Host != "" {
				namespace = u.Host
			}
			entries = append(entries, primitive.CatalogItem[json.RawMessage]{Name: item.ID, Description: strings.TrimSpace(item.Name + " " + item.Description), Namespace: namespace, URI: item.URI, OwnerID: item.ID, Value: value})
		}
		return entries, nil
	}
	var index *primitive.Catalog[json.RawMessage]
	owner, hasOwner := s.mgr.(playbookCatalogOwner)
	if hasOwner {
		conversation := runtimerequestctx.ConversationIDFromContext(ctx)
		scope, cacheable, err := owner.CatalogScope(ctx, conversation, nil)
		if err != nil {
			return err
		}
		until := time.Now().Add(30 * time.Second)
		if !cacheable {
			until = time.Now()
		}
		index, err = owner.VisibleCatalog(ctx, fmt.Sprintf("playbook:%p:%s:%x", s.repo, scope, digest), until, load)
		if err != nil {
			return err
		}
	} else {
		entries, err := load(ctx)
		if err != nil {
			return err
		}
		identity, _ := json.Marshal([]string{authctx.CanonicalUserID(ctx), authctx.EffectiveUserID(ctx), authctx.Provider(ctx), runtimerequestctx.ConversationIDFromContext(ctx)})
		index = primitive.NewCatalog(fmt.Sprintf("playbook:%x:%x", sha256.Sum256(identity), digest), entries)
	}
	page, err := index.Page(ctx, query)
	if err != nil {
		return err
	}
	profiles := make([]ListItem, 0, len(page.Items))
	for _, raw := range page.Items {
		var item ListItem
		if err = json.Unmarshal(raw, &item); err != nil {
			return err
		}
		profiles = append(profiles, item)
	}
	if hasOwner {
		if err = owner.CheckVisibleCatalog(ctx, index); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	out.Profiles = profiles
	out.NextCursor, out.Complete, out.Total = page.NextCursor, page.Complete, page.Total
	return nil
}
