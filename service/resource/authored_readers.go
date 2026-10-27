package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	intent "github.com/viant/agently-core/protocol/intent"
	identity "github.com/viant/agently-core/protocol/resource"
	tpl "github.com/viant/agently-core/protocol/template"
	bundle "github.com/viant/agently-core/protocol/templatebundle"
)

// TemplateDefinition keeps the original DTO at its original JSON shape.
// Bundle metadata is an owning-definition attachment, not a second resource kind.
type TemplateDefinition struct {
	tpl.Template
	TemplateBundles []*bundle.Bundle `json:"templateBundles,omitempty"`
}
type TemplateReader struct{ Gateway *Gateway }
type IntentReader struct {
	Gateway          *Gateway
	includePlaybooks bool
	// Scope resolves relative profile IDs within the host's authored workspace.
	// Absolute resource URIs remain federated references.
	Scope DefinitionScope
}
type TemplateBundleReader struct{ Gateway *Gateway }

func NewTemplateReader(g *Gateway) *TemplateReader { return &TemplateReader{Gateway: g} }
func NewIntentReader(g *Gateway) *IntentReader     { return &IntentReader{Gateway: g} }
func NewPlaybookReader(g *Gateway) *IntentReader {
	return &IntentReader{Gateway: g, includePlaybooks: true}
}
func NewTemplateBundleReader(g *Gateway) *TemplateBundleReader {
	return &TemplateBundleReader{Gateway: g}
}
func decodeAuthored[T any](raw json.RawMessage) (T, error) {
	var result T
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&result); err != nil {
		return result, err
	}
	if d.Decode(new(any)) != io.EOF {
		return result, identity.ErrResource
	}
	return result, nil
}
func DecodeTemplateDefinition(raw json.RawMessage) (TemplateDefinition, error) {
	v, err := decodeAuthored[TemplateDefinition](raw)
	if err != nil {
		return v, err
	}
	return v, v.Template.Validate()
}
func DecodeIntentDefinition(raw json.RawMessage) (intent.Profile, error) {
	v, err := decodeAuthored[intent.Profile](raw)
	if err != nil {
		return v, err
	}
	if strings.TrimSpace(v.ID) == "" {
		return v, identity.ErrResource
	}
	if err := v.Validate(); err != nil {
		return v, err
	}
	seen := map[string]bool{}
	for _, b := range v.ToolBundleDefinitions {
		if b == nil {
			return v, identity.ErrResource
		}
		if err := b.Validate(); err != nil {
			return v, err
		}
		key := strings.ToLower(strings.TrimSpace(b.ID))
		if seen[key] {
			return v, ErrCollision
		}
		seen[key] = true
	}
	return v, nil
}
func DecodePlaybookDefinition(raw json.RawMessage) (intent.Profile, error) {
	return DecodeIntentDefinition(raw)
}
func authoredPages[T any](ctx context.Context, g *Gateway, kind string) ([]Located[T], error) {
	var out []Located[T]
	q := CatalogQuery{Limit: 100}
	for {
		p, err := ListPage[T](ctx, g, kind, q, true)
		if err != nil {
			return nil, err
		}
		scope := DefinitionScopeFromContext(ctx)
		for _, row := range p.Items {
			if (scope.Namespace == "" || row.Resource.Namespace == scope.Namespace) && (scope.ProviderIdentity == "" || row.Connection.ProviderIdentity == scope.ProviderIdentity) {
				out = append(out, row)
			}
		}
		if p.Complete {
			return out, nil
		}
		if p.NextCursor == "" {
			return nil, identity.ErrResource
		}
		q.Cursor = p.NextCursor
	}
}

// LoadAll returns only matching/listing metadata. It is never body authority.
func (r *TemplateReader) LoadAll(ctx context.Context) ([]*tpl.Template, error) {
	type summary struct {
		ID, Name, Description, Format               string
		AppliesTo, Platforms, FormFactors, Surfaces []string
	}
	rows, err := authoredPages[summary](ctx, r.Gateway, "template")
	if err != nil {
		return nil, err
	}
	out := make([]*tpl.Template, 0, len(rows))
	for _, row := range rows {
		v := row.Definition
		if v.ID == "" || v.Name == "" {
			continue
		}
		out = append(out, &tpl.Template{ResourceURI: row.Resource.URI, ID: v.ID, Name: v.Name, Description: v.Description, Format: v.Format, AppliesTo: v.AppliesTo, Platforms: v.Platforms, FormFactors: v.FormFactors, Surfaces: v.Surfaces})
	}
	return out, nil
}
func (r *TemplateReader) Load(ctx context.Context, key string) (*tpl.Template, error) {
	got, err := ResolveDefinition[TemplateDefinition](ctx, r.Gateway, "template", key, func(v TemplateDefinition) string {
		if strings.EqualFold(v.ID, key) || strings.EqualFold(v.Name, key) {
			return key
		}
		return ""
	}, DecodeTemplateDefinition)
	if err != nil {
		return nil, err
	}
	got.Definition.Template.ResourceURI = got.Resource.URI
	return &got.Definition.Template, nil
}
func (r *IntentReader) LoadAll(ctx context.Context) ([]*intent.Profile, error) {
	type summary struct {
		ID, Name, Description, Model, Template                       string
		AppliesTo, Templates, ToolBundles, PreferredTools, Resources []string
		EvidenceContract                                             *intent.EvidenceContract
		Knowledge                                                    []intent.KnowledgeMatch
	}
	var out []*intent.Profile
	kinds := []string{"intent"}
	if r.includePlaybooks {
		kinds = []string{"playbook", "intent"}
	}
	for _, kind := range kinds {
		rows, err := authoredPages[summary](ctx, r.Gateway, kind)
		if errors.Is(err, ErrUnavailable) || errors.Is(err, identity.ErrResourceDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			v := row.Definition
			if v.ID == "" {
				continue
			}
			out = append(out, &intent.Profile{ResourceURI: row.Resource.URI, ID: v.ID, Name: v.Name, Description: v.Description, Model: v.Model, Template: v.Template, AppliesTo: v.AppliesTo, Templates: v.Templates, ToolBundles: v.ToolBundles, PreferredTools: v.PreferredTools, Resources: v.Resources, EvidenceContract: v.EvidenceContract, Knowledge: v.Knowledge})
		}
	}
	return out, nil
}
func (r *IntentReader) Load(ctx context.Context, key string) (*intent.Profile, error) {
	if r.Scope != (DefinitionScope{}) {
		ctx = WithDefinitionScope(ctx, r.Scope)
	}
	kinds := []string{"intent"}
	if r.includePlaybooks {
		kinds = []string{"playbook", "intent"}
	}
	if uri, err := identity.ParseResourceURI(key); err == nil {
		if uri.Kind != "playbook" && uri.Kind != "intent" {
			return nil, identity.ErrResource
		}
		kinds = []string{uri.Kind}
	}
	var got *Result[intent.Profile]
	for _, kind := range kinds {
		selected, err := ResolveDefinition[intent.Profile](ctx, r.Gateway, kind, key, func(v intent.Profile) string {
			if strings.EqualFold(v.ID, key) {
				return key
			}
			return ""
		}, DecodeIntentDefinition)
		if errors.Is(err, ErrUnavailable) || errors.Is(err, identity.ErrResourceDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if got != nil {
			return nil, ErrCollision
		}
		got = selected
	}
	if got == nil {
		return nil, identity.ErrResourceDenied
	}
	for _, m := range got.Definition.Messages {
		if m.URI != "" {
			return nil, fmt.Errorf("intent supporting text must be materialized by its provider")
		}
	}
	got.Definition.ResourceURI = got.Resource.URI
	got.Definition.ResourceProviderIdentity = got.ResolvedResource.ProviderIdentity
	got.Definition.ResourceFingerprint = got.Resource.ContentFingerprint
	return &got.Definition, nil
}
func (r *TemplateBundleReader) LoadAll(ctx context.Context) ([]*bundle.Bundle, error) {
	type projection struct {
		TemplateBundles []*bundle.Bundle `json:"templateBundles"`
	}
	rows, err := authoredPages[projection](ctx, r.Gateway, "template")
	if err != nil {
		return nil, err
	}
	out := []*bundle.Bundle{}
	seen := map[string]string{}
	for _, row := range rows {
		for _, b := range row.Definition.TemplateBundles {
			if b == nil {
				return nil, identity.ErrResource
			}
			if err := b.Validate(); err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(b)
			fp := identity.ContentFingerprint(raw)
			if old, ok := seen[b.ID]; ok {
				if old != fp {
					return nil, ErrCollision
				}
				continue
			}
			seen[b.ID] = fp
			out = append(out, b)
		}
	}
	return out, nil
}

// LoadFrom preserves explicit canonical references; only unqualified aliases
// are confined to the admitted parent's namespace and provider.
func (r *TemplateReader) LoadFrom(ctx context.Context, key, parentURI, provider string) (*tpl.Template, error) {
	parent, err := identity.ParseResourceURI(parentURI)
	if err != nil || parent.Kind != "intent" && parent.Kind != "playbook" {
		return nil, identity.ErrResource
	}
	got, err := ResolveDefinitionIn[TemplateDefinition](ctx, r.Gateway, "template", key, DefinitionScope{Namespace: parent.Namespace, ProviderIdentity: provider}, func(v TemplateDefinition) string {
		if strings.EqualFold(v.ID, key) || strings.EqualFold(v.Name, key) {
			return key
		}
		return ""
	}, DecodeTemplateDefinition)
	if err != nil {
		return nil, err
	}
	got.Definition.Template.ResourceURI = got.Resource.URI
	return &got.Definition.Template, nil
}
