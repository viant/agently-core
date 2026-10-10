package resource

import (
	"context"

	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
)

// GetForProvider combines exact owner routing with Get's complete fresh
// verification. Resolving a configured routing claim requires no additional
// namespace discovery before the immediately following verified Get.
func (g *Gateway) GetForProvider(ctx context.Context, owner string, ref identity.ResourceRef, pin *identity.ResolvedResource) (*primitive.GetResult, error) {
	result, _, err := g.getForProvider(ctx, owner, ref, pin)
	return result, err
}

func (g *Gateway) getForProvider(ctx context.Context, owner string, ref identity.ResourceRef, pin *identity.ResolvedResource) (*primitive.GetResult, Connection, error) {
	if owner == "" || pin != nil && pin.ProviderIdentity != owner {
		return nil, Connection{}, identity.ErrResourceDenied
	}
	connection, found, err := g.configuredOwnerConnection(ctx, owner)
	if err != nil {
		return nil, Connection{}, err
	}
	if !found {
		connection, err = g.ConnectionForProvider(ctx, owner)
		if err != nil {
			return nil, Connection{}, err
		}
	}
	result, err := g.Get(ctx, connection, ref, pin)
	return result, connection, err
}
