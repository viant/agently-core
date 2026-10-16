package sdk

import (
	"context"
	"encoding/json"
	"strings"

	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	preset "github.com/viant/agently-core/service/elicitation/refiner"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp-protocol/schema"
)

func (c *backendClient) lookupOwner(ctx context.Context, in *api.ListLookupRegistryInput) (*identity.ResolvedResource, error) {
	if in.Owner == nil {
		return nil, nil
	}
	uri, err := identity.ParseResourceURI(in.Owner.URI)
	if err != nil || (uri.Kind != "window" && uri.Kind != "template" && uri.Kind != "intent" && uri.Kind != "lookup") {
		return nil, identity.ErrResourceDenied
	}
	connection, err := c.primitiveProviders.ConnectionForResource(ctx, uri, strings.TrimSpace(in.ProviderIdentity))
	if err != nil {
		return nil, err
	}
	got, err := resources.Get[json.RawMessage](ctx, c.primitiveProviders, connection, *in.Owner, nil)
	if err != nil {
		return nil, err
	}
	return got.ResolvedResource, nil
}

func (c *backendClient) configureLookupRefiner() {
	if c.primitiveProviders == nil || c.elicSvc == nil {
		return
	}
	c.elicSvc.ConfigureContextRefiner(func(ctx context.Context, rs *schema.ElicitRequestParamsRequestedSchema) error {
		if owner, ok := requestctx.ResolvedResourceFromContext(ctx); ok {
			service, err := c.primitiveLookupService(ctx, owner, "")
			if err != nil {
				return err
			}
			uri, err := identity.ParseResourceURI(owner.URI)
			if err != nil {
				return err
			}
			preset.RefineWithHook(rs, service.NewElicitationHook(uri.Kind, uri.Name))
			return nil
		}
		service, err := c.primitiveLookupService(ctx, nil, "")
		if err != nil {
			return err
		}
		preset.RefineWithHook(rs, service.NewWildcardHook())
		return nil
	})
}
