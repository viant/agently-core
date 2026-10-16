package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"strings"
)

func feedIdentity(spec *FeedSpec) string {
	if spec == nil {
		return ""
	}
	if spec.URI != "" {
		return spec.URI
	}
	return spec.ID
}
func normalizeFeedSpec(spec *FeedSpec) {
	if spec.Title == "" {
		if ui, ok := spec.UI.(map[string]interface{}); ok {
			spec.Title, _ = ui["title"].(string)
		}
		if spec.Title == "" {
			spec.Title = cases.Title(language.English).String(spec.ID)
		}
	}
	if spec.Presentation != nil {
		spec.Presentation = normalizedFeedPresentation(spec)
	}
}

type feedMatchMetadata struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	DeveloperOnly bool              `json:"developerOnly"`
	Presentation  *FeedPresentation `json:"presentation"`
	Match         FeedMatch         `json:"match"`
	Activation    FeedActivation    `json:"activation"`
	UI            struct {
		Title string `json:"title"`
	} `json:"ui"`
}

// feedSpecs is private matching metadata, retained only in the manager's
// existing actor/session-scoped snapshot. It is never an authority for delivery.
func (c *backendClient) feedSpecs(ctx context.Context) ([]*FeedSpec, error) {
	if c.primitiveProviders == nil {
		if c.feeds == nil {
			return nil, nil
		}
		return c.feeds.Specs(), nil
	}
	var specs []*FeedSpec
	seen := map[string]bool{}
	query := resources.CatalogQuery{Limit: 100}
	for {
		page, err := resources.ListPage[feedMatchMetadata](ctx, c.primitiveProviders, primitive.ToolfeedKind, query, true)
		if err != nil {
			return nil, err
		}
		for _, row := range page.Items {
			if seen[row.Resource.URI] {
				return nil, resources.ErrCollision
			}
			seen[row.Resource.URI] = true
			definition := row.Definition
			spec := FeedSpec{ID: definition.ID, Title: definition.Title, DeveloperOnly: definition.DeveloperOnly, Presentation: definition.Presentation, Match: definition.Match, Activation: definition.Activation}
			if spec.Title == "" {
				spec.Title = definition.UI.Title
			}
			spec.URI = row.Resource.URI
			spec.ProviderIdentity = row.Connection.ProviderIdentity
			spec.DefinitionFingerprint = row.Resource.ContentFingerprint
			spec.DefinitionRevision = row.Resource.Revision
			normalizeFeedSpec(&spec)
			specs = append(specs, &spec)
		}
		if page.Complete {
			return specs, nil
		}
		if page.NextCursor == "" {
			return nil, identity.ErrResource
		}
		query.Cursor = page.NextCursor
	}
}
func selectFeedSpec(specs []*FeedSpec, id string) (*FeedSpec, error) {
	var selected *FeedSpec
	for _, spec := range specs {
		if spec != nil && (feedIdentity(spec) == id || spec.ID == id) {
			if selected != nil {
				return nil, resources.ErrCollision
			}
			selected = spec
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("feed %q not found", id)
	}
	return selected, nil
}
func (c *backendClient) admitFeedSpec(ctx context.Context, spec *FeedSpec) (*FeedSpec, error) {
	if c.primitiveProviders == nil {
		return spec, nil
	}
	uri, err := identity.ParseResourceURI(spec.URI)
	if err != nil {
		return nil, err
	}
	connection, err := c.primitiveProviders.ConnectionForResource(ctx, uri, spec.ProviderIdentity)
	if err != nil {
		return nil, err
	}
	got, err := resources.Get[FeedSpec](ctx, c.primitiveProviders, connection, identity.ResourceRef{URI: spec.URI, Revision: spec.DefinitionRevision}, nil, decodeFeedSpec)
	if err != nil {
		return nil, err
	}
	if spec.DefinitionFingerprint != "" && spec.DefinitionFingerprint != got.Resource.ContentFingerprint {
		return nil, identity.ErrResourceStale
	}
	fresh := got.Definition
	fresh.URI = got.Resource.URI
	fresh.ProviderIdentity = got.ResolvedResource.ProviderIdentity
	fresh.DefinitionFingerprint = got.Resource.ContentFingerprint
	fresh.DefinitionRevision = got.Resource.Revision
	normalizeFeedSpec(&fresh)
	return &fresh, nil
}

// ListFeedSpecsContext returns summaries only. Full UI/datasource definitions
// require GetFeedSpecContext's fresh selected-provider Get.
func (c *backendClient) ListFeedSpecsContext(ctx context.Context) ([]*FeedSpec, error) {
	specs, err := c.feedSpecs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*FeedSpec, 0, len(specs))
	for _, spec := range specs {
		copy := *spec
		copy.UI = nil
		copy.DataSource = nil
		out = append(out, &copy)
	}
	return out, nil
}
func decodeFeedSpec(raw json.RawMessage) (FeedSpec, error) {
	var spec FeedSpec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&spec)
	return spec, err
}
func (c *backendClient) GetFeedSpecContext(ctx context.Context, id string) (*FeedSpec, error) {
	if c.primitiveProviders != nil {
		got, err := resources.ResolveDefinition[FeedSpec](ctx, c.primitiveProviders, primitive.ToolfeedKind, strings.TrimSpace(id), func(spec FeedSpec) string { return spec.ID }, decodeFeedSpec)
		if err != nil {
			return nil, err
		}
		spec := got.Definition
		spec.URI = got.Resource.URI
		spec.ProviderIdentity = got.ResolvedResource.ProviderIdentity
		spec.DefinitionFingerprint = got.Resource.ContentFingerprint
		spec.DefinitionRevision = got.Resource.Revision
		normalizeFeedSpec(&spec)
		return &spec, nil
	}
	specs, err := c.feedSpecs(ctx)
	if err != nil {
		return nil, err
	}
	return selectFeedSpec(specs, strings.TrimSpace(id))
}
func (c *backendClient) matchFeedSpecsContext(ctx context.Context, name string) ([]*FeedSpec, error) {
	specs, err := c.feedSpecs(ctx)
	if err != nil {
		return nil, err
	}
	service, method := parseToolName(name)
	var result []*FeedSpec
	for _, spec := range specs {
		if matchesRule(spec.Match, service, method) {
			fresh, err := c.admitFeedSpec(ctx, spec)
			if err != nil {
				return nil, err
			}
			if matchesRule(fresh.Match, service, method) {
				result = append(result, fresh)
			}
		}
	}
	return result, nil
}
func listClientFeedSpecs(ctx context.Context, client any) ([]*FeedSpec, error) {
	if backend, ok := client.(interface {
		ListFeedSpecsContext(context.Context) ([]*FeedSpec, error)
	}); ok {
		return backend.ListFeedSpecsContext(ctx)
	}
	if backend, ok := client.(interface{ ListFeedSpecs() []*FeedSpec }); ok {
		return backend.ListFeedSpecs(), nil
	}
	return nil, fmt.Errorf("feed registry is not configured")
}
func getClientFeedSpec(ctx context.Context, client any, id string) (*FeedSpec, error) {
	if backend, ok := client.(interface {
		GetFeedSpecContext(context.Context, string) (*FeedSpec, error)
	}); ok {
		return backend.GetFeedSpecContext(ctx, id)
	}
	specs, err := listClientFeedSpecs(ctx, client)
	if err != nil {
		return nil, err
	}
	return selectFeedSpec(specs, id)
}
