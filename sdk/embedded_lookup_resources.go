package sdk

import (
	"context"
	"fmt"

	identity "github.com/viant/agently-core/protocol/resource"
	overlays "github.com/viant/agently-core/service/lookup/overlay"
	resources "github.com/viant/agently-core/service/resource"
)

func (c *backendClient) primitiveLookupService(ctx context.Context, owner *identity.ResolvedResource, provider string) (*overlays.Service, error) {
	scope := resources.DefinitionScope{ProviderIdentity: provider}
	if owner != nil {
		scope.ProviderIdentity = owner.ProviderIdentity
	}
	definitions, err := resources.NewLookupReader(c.primitiveProviders).LoadAll(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("lookup catalog: %w", err)
	}
	primary := overlays.New(lookupDefinitionStore{definitions})
	if owner == nil {
		return primary, nil
	}
	attached, err := overlays.ReadAttachments(ctx, c.primitiveProviders, *owner)
	if err != nil {
		return nil, fmt.Errorf("lookup owner attachments: %w", err)
	}
	return overlays.Combine(primary, attached), nil
}
