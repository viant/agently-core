package template

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	tpldef "github.com/viant/agently-core/protocol/template"
	svc "github.com/viant/agently-core/protocol/tool/service"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/shared/toolexec"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	tplbundlerepo "github.com/viant/agently-core/workspace/repository/templatebundle"
)

const Name = "template"

type ListInput struct{}

type ListItem struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
}

type ListOutput struct {
	Items []ListItem `json:"items"`
}

type GetInput struct {
	Name            string `json:"name"`
	IncludeDocument *bool  `json:"includeDocument,omitempty"`
}

type GetOutput struct {
	Name             string                 `json:"name,omitempty"`
	Format           string                 `json:"format,omitempty"`
	Description      string                 `json:"description,omitempty"`
	Instructions     string                 `json:"instructions,omitempty"`
	Schema           map[string]interface{} `json:"schema,omitempty"`
	IncludedDocument bool                   `json:"includedDocument,omitempty"`
}

type Service struct {
	templates *tplrepo.Repository
	bundles   *tplbundlerepo.Repository
	conv      apiconv.Client
	finder    agentmdl.Finder
}

func New(templates *tplrepo.Repository, bundles *tplbundlerepo.Repository, opts ...func(*Service)) *Service {
	s := &Service{templates: templates, bundles: bundles}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

func WithConversationClient(c apiconv.Client) func(*Service) { return func(s *Service) { s.conv = c } }
func WithAgentFinder(f agentmdl.Finder) func(*Service)       { return func(s *Service) { s.finder = f } }

func (s *Service) Name() string { return Name }

func (s *Service) Methods() svc.Signatures {
	return []svc.Signature{
		{Name: "list", Description: "List available output templates by name and description", Input: reflect.TypeOf(&ListInput{}), Output: reflect.TypeOf(&ListOutput{})},
		{Name: "get", Description: "Get an output template by name and optionally inject it as a system document for the next model step", Input: reflect.TypeOf(&GetInput{}), Output: reflect.TypeOf(&GetOutput{})},
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
	templates, err := s.allowedTemplates(ctx)
	if err != nil {
		return err
	}
	items := make([]ListItem, 0, len(templates))
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		items = append(items, ListItem{
			Name:        strings.TrimSpace(tpl.Name),
			Description: strings.TrimSpace(tpl.Description),
			Format:      strings.TrimSpace(tpl.Format),
		})
	}
	sort.Slice(items, func(i, j int) bool { return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name) })
	lo.Items = items
	return nil
}

func (s *Service) get(ctx context.Context, in, out interface{}) error {
	gi, ok := in.(*GetInput)
	if !ok {
		return svc.NewInvalidInputError(in)
	}
	goo, ok := out.(*GetOutput)
	if !ok {
		return svc.NewInvalidOutputError(out)
	}
	name := strings.TrimSpace(gi.Name)
	if name == "" {
		return fmt.Errorf("template name is required")
	}
	templates, err := s.allowedTemplates(ctx)
	if err != nil {
		return err
	}
	var selected *tpldef.Template
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(tpl.Name), name) || strings.EqualFold(strings.TrimSpace(tpl.ID), name) {
			selected = tpl
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("template %q not found", name)
	}
	goo.Name = strings.TrimSpace(selected.Name)
	goo.Format = strings.TrimSpace(selected.Format)
	goo.Description = strings.TrimSpace(selected.Description)
	goo.Instructions = strings.TrimSpace(selected.Instructions)
	goo.Schema = selected.Schema
	if gi.IncludeDocument != nil && *gi.IncludeDocument {
		if err := s.injectTemplateDocument(ctx, selected); err != nil {
			return err
		}
		goo.IncludedDocument = true
	}
	return nil
}

func (s *Service) allowedTemplates(ctx context.Context) ([]*tpldef.Template, error) {
	if s == nil || s.templates == nil {
		return nil, fmt.Errorf("template repository not configured")
	}
	allTemplates, err := s.templates.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	if len(allTemplates) == 0 {
		return nil, nil
	}
	agentID := s.currentAgentID(ctx)
	if agentID == "" || s.finder == nil || s.bundles == nil {
		return s.filterTemplatesForCurrentTarget(ctx, allTemplates), nil
	}
	ag, err := s.finder.Find(ctx, agentID)
	if err != nil || ag == nil || len(ag.Template.Bundles) == 0 {
		return s.filterTemplatesForCurrentTarget(ctx, allTemplates), nil
	}
	allBundles, err := s.bundles.LoadAll(ctx)
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{}
	for _, bundleID := range ag.Template.Bundles {
		for _, bundle := range allBundles {
			if bundle == nil || !strings.EqualFold(strings.TrimSpace(bundle.ID), strings.TrimSpace(bundleID)) {
				continue
			}
			for _, templateName := range bundle.Templates {
				allowed[strings.ToLower(strings.TrimSpace(templateName))] = struct{}{}
			}
		}
	}
	if len(allowed) == 0 {
		return nil, nil
	}
	filtered := make([]*tpldef.Template, 0, len(allTemplates))
	for _, tpl := range allTemplates {
		if tpl == nil {
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(tpl.Name))]; ok {
			filtered = append(filtered, tpl)
			continue
		}
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(tpl.ID))]; ok {
			filtered = append(filtered, tpl)
		}
	}
	return s.filterTemplatesForCurrentTarget(ctx, filtered), nil
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

func (s *Service) injectTemplateDocument(ctx context.Context, tpl *tpldef.Template) error {
	if s == nil || s.conv == nil || tpl == nil {
		return nil
	}
	turn, ok := runtimerequestctx.TurnMetaFromContext(ctx)
	if !ok || strings.TrimSpace(turn.ConversationID) == "" || strings.TrimSpace(turn.TurnID) == "" {
		return nil
	}
	content := strings.TrimSpace(tpl.Instructions)
	if content == "" {
		return nil
	}
	_, err := apiconv.AddMessage(ctx, s.conv, &turn,
		apiconv.WithRole("system"),
		apiconv.WithType("text"),
		apiconv.WithCreatedByUserID("template"),
		apiconv.WithMode(toolexec.SystemDocumentMode),
		apiconv.WithContent(content),
		apiconv.WithTags(toolexec.SystemDocumentTag),
		apiconv.WithContextSummary("template://"+strings.TrimSpace(tpl.Name)),
		apiconv.WithCreatedAt(time.Now()),
	)
	return err
}

func (s *Service) filterTemplatesForCurrentTarget(ctx context.Context, templates []*tpldef.Template) []*tpldef.Template {
	if len(templates) == 0 {
		return templates
	}
	target := s.currentClientTarget(ctx)
	if target.platform == "" && target.formFactor == "" && target.surface == "" {
		return templates
	}
	filtered := make([]*tpldef.Template, 0, len(templates))
	for _, tpl := range templates {
		if tpl == nil {
			continue
		}
		if !matchesTarget(strings.TrimSpace(target.platform), tpl.Platforms) {
			continue
		}
		if !matchesTarget(strings.TrimSpace(target.formFactor), tpl.FormFactors) {
			continue
		}
		if !matchesTarget(strings.TrimSpace(target.surface), tpl.Surfaces) {
			continue
		}
		filtered = append(filtered, tpl)
	}
	return filtered
}

type clientTarget struct {
	platform   string
	formFactor string
	surface    string
}

func (s *Service) currentClientTarget(ctx context.Context) clientTarget {
	if s == nil || s.conv == nil {
		return clientTarget{}
	}
	convID := strings.TrimSpace(runtimerequestctx.ConversationIDFromContext(ctx))
	if convID == "" {
		return clientTarget{}
	}
	conv, err := s.conv.GetConversation(ctx, convID)
	if err != nil || conv == nil || conv.Metadata == nil || strings.TrimSpace(*conv.Metadata) == "" {
		return clientTarget{}
	}
	var meta struct {
		Context map[string]interface{} `json:"context"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(*conv.Metadata)), &meta); err != nil {
		return clientTarget{}
	}
	client, ok := meta.Context["client"].(map[string]interface{})
	if !ok || len(client) == 0 {
		return clientTarget{}
	}
	return clientTarget{
		platform:   stringValue(client["platform"]),
		formFactor: stringValue(client["formFactor"]),
		surface:    stringValue(client["surface"]),
	}
}

func matchesTarget(current string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	current = strings.ToLower(strings.TrimSpace(current))
	if current == "" {
		return false
	}
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if candidate == "*" || candidate == "any" || candidate == current {
			return true
		}
	}
	return false
}

func stringValue(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
