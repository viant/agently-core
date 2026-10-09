package resource

import (
	"context"
	identity "github.com/viant/agently-core/protocol/resource"
	service "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/forge/backend/types"
	"strings"
)

// CompositeWindowCatalog keeps one host open path while retaining the local
// authoritative catalog and explicit delegated provider locators.
type CompositeWindowCatalog struct {
	Local                 service.WindowDefinitionCatalog
	LocalProviderIdentity string
	Remote                *WindowCatalog
}

func (c *CompositeWindowCatalog) AuthzReady() bool {
	if c == nil || !c.Remote.AuthzReady() {
		return false
	}
	if c.Local == nil {
		return true
	}
	ready, ok := c.Local.(interface{ AuthzReady() bool })
	return ok && ready.AuthzReady()
}
func (c *CompositeWindowCatalog) UsesResourceResolution() bool { return true }
func (c *CompositeWindowCatalog) List(ctx context.Context, input *service.WindowDefinitionListInput) (*service.WindowDefinitionListOutput, error) {
	if !c.AuthzReady() {
		return nil, identity.ErrResourceDenied
	}
	if input == nil {
		input = &service.WindowDefinitionListInput{}
	}
	if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
		return nil, identity.ErrResource
	}
	query := input.Query
	out := []service.WindowDefinitionSummary{}
	seen := map[string]bool{}
	appendCatalog := func(catalog service.WindowDefinitionCatalog) error {
		if catalog == nil {
			return nil
		}
		for offset := 0; offset < 10000; offset += 100 {
			page, e := catalog.List(ctx, &service.WindowDefinitionListInput{Query: query, Offset: offset, Limit: 100})
			if e != nil {
				return e
			}
			if page == nil {
				return identity.ErrResource
			}
			for _, row := range page.Windows {
				key := row.ResourceURI
				if key == "" {
					key = row.WindowID
				}
				if seen[key] {
					return ErrCollision
				}
				seen[key] = true
				out = append(out, row)
			}
			if !page.HasMore {
				return nil
			}
			if len(page.Windows) == 0 {
				return identity.ErrResource
			}
		}
		return identity.ErrResource
	}
	if e := appendCatalog(c.Local); e != nil {
		return nil, e
	}
	if e := appendCatalog(c.Remote); e != nil {
		return nil, e
	}
	start := input.Offset
	if start > len(out) {
		start = len(out)
	}
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	end := start + limit
	if end > len(out) {
		end = len(out)
	}
	return &service.WindowDefinitionListOutput{Windows: out[start:end], HasMore: end < len(out)}, nil
}
func (c *CompositeWindowCatalog) Get(ctx context.Context, input *service.WindowDefinitionGetInput) (*service.WindowDefinitionGetOutput, error) {
	if input == nil {
		return nil, identity.ErrResource
	}
	if strings.HasPrefix(input.WindowID, locatorPrefix) {
		return c.Remote.Get(ctx, input)
	}
	if uri, e := identity.ParseResourceURI(input.WindowID); e == nil && uri.Kind == "window" {
		rows, e := c.List(ctx, &service.WindowDefinitionListInput{})
		if e != nil {
			return nil, e
		}
		var selected string
		for _, row := range rows.Windows {
			if row.ResourceURI == uri.String() {
				if selected != "" {
					return nil, ErrCollision
				}
				selected = row.WindowID
			}
		}
		if selected == "" {
			return nil, identity.ErrResourceDenied
		}
		copy := *input
		copy.WindowID = selected
		return c.Get(ctx, &copy)
	}
	if c.Local == nil {
		return nil, identity.ErrResourceDenied
	}
	return c.Local.Get(ctx, input)
}

func (c *CompositeWindowCatalog) RevalidateWindowResource(ctx context.Context, key string, pin identity.ResolvedResource, target *types.WindowTarget) (*identity.ResolvedResource, error) {
	result, err := c.Get(ctx, &service.WindowDefinitionGetInput{WindowID: key, ResolvedResource: &pin, Target: target})
	if err != nil {
		return nil, err
	}
	return result.Definition.Resource, nil
}
func (c *CompositeWindowCatalog) RevalidateResource(ctx context.Context, key string, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
	return c.RevalidateWindowResource(ctx, key, pin, nil)
}
func (c *CompositeWindowCatalog) ResourceReference(ctx context.Context, key string) (identity.ResourceRef, error) {
	if strings.HasPrefix(key, locatorPrefix) {
		return c.Remote.ResourceReference(ctx, key)
	}
	if uri, e := identity.ParseResourceURI(key); e == nil && uri.Kind == "window" {
		rows, e := c.List(ctx, &service.WindowDefinitionListInput{})
		if e != nil {
			return identity.ResourceRef{}, e
		}
		for _, row := range rows.Windows {
			if row.ResourceURI == key {
				return identity.ResourceRef{URI: key}, nil
			}
		}
		return identity.ResourceRef{}, identity.ErrResourceDenied
	}
	local, ok := c.Local.(interface {
		ResourceReference(context.Context, string) (identity.ResourceRef, error)
	})
	if !ok {
		return identity.ResourceRef{}, identity.ErrResourceDenied
	}
	return local.ResourceReference(ctx, key)
}
func (c *CompositeWindowCatalog) CheckWindowAdmission(ctx context.Context, key string) (bool, error) {
	if strings.HasPrefix(key, locatorPrefix) {
		return c.Remote.CheckWindowAdmission(ctx, key)
	}
	checker, ok := c.Local.(interface {
		CheckWindowAdmission(context.Context, string) (bool, error)
	})
	if !ok {
		return false, identity.ErrResourceDenied
	}
	return checker.CheckWindowAdmission(ctx, key)
}
func (c *CompositeWindowCatalog) VerifyWindowTarget(ctx context.Context, pin identity.ResolvedResource, target types.WindowTarget, variant string) error {
	if pin.ProviderIdentity != c.LocalProviderIdentity {
		return c.Remote.VerifyWindowTarget(ctx, pin, target, variant)
	}
	local, ok := c.Local.(interface {
		VerifyWindowTarget(context.Context, identity.ResolvedResource, types.WindowTarget, string) error
	})
	if !ok {
		return identity.ErrResourceDenied
	}
	return local.VerifyWindowTarget(ctx, pin, target, variant)
}
