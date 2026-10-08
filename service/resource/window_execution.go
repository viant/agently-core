package resource

import (
	"context"
	"encoding/json"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	primitive "github.com/viant/agently-core/protocol/primitive"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/forge/backend/types"
)

func (g *Gateway) ConnectionForProvider(ctx context.Context, owner string) (Connection, error) {
	return g.dependencyConnection(ctx, Connection{}, owner)
}
func (g *Gateway) ConnectionForResource(ctx context.Context, uri identity.ResourceURI, owner string) (Connection, error) {
	discovered, e := g.Discover(ctx)
	if e != nil {
		return Connection{}, e
	}
	var found Connection
	for _, ns := range discovered {
		if ns.Namespace != uri.Namespace || owner != "" && ns.Connection.ProviderIdentity != owner {
			continue
		}
		caps := capabilities{namespaces: []primitive.NamespaceCapabilities{ns.NamespaceCapabilities}}
		if !supports(caps, uri.Namespace, uri.Kind, "get") {
			continue
		}
		if found.Name != "" && found != ns.Connection {
			return Connection{}, ErrCollision
		}
		found = ns.Connection
	}
	if found.Name == "" {
		return found, identity.ErrResourceDenied
	}
	return found, nil
}

// VerifyDependencies is suitable for PreparedAuthorization's explicit child
// verifier. The signed target binds the supplied pins; pins alone grant nothing.
func (c *WindowCatalog) VerifyDependencies(ctx context.Context, parent identity.ResolvedResource, target *types.WindowTarget, variant *types.WindowResourceVariant) error {
	if !c.AuthzReady() || target == nil || variant == nil || target.SelectionToken == "" {
		return identity.ErrResourceDenied
	}
	if e := c.TargetProof.Verify(ctx, parent, *target, variant.Fingerprint, target.SelectionToken); e != nil {
		return e
	}
	if len(variant.DataSourceResources) == 0 {
		if len(target.DependencyPins) > 0 {
			return identity.ErrResourceDenied
		}
		return nil
	}
	if len(target.DependencyPins) != len(variant.DataSourceResources) {
		return identity.ErrResourceDenied
	}
	connection, e := c.Gateway.ConnectionForProvider(ctx, parent.ProviderIdentity)
	if e != nil {
		return e
	}
	_, e = c.Gateway.ResolveDependencyPins(ctx, connection, parent, variant.DataSourceResources, variant.DataSources, target.DependencyPins)
	return e
}

func (c *WindowCatalog) ResolveDatasource(ctx context.Context, parent identity.ResolvedResource, target *types.WindowTarget, id string) (*dsproto.DataSource, error) {
	if !c.AuthzReady() {
		return nil, identity.ErrResourceDenied
	}
	connection, e := c.Gateway.ConnectionForProvider(ctx, parent.ProviderIdentity)
	if e != nil {
		return nil, e
	}
	definition, e := c.Gateway.Get(ctx, connection, identity.ResourceRef{URI: parent.URI, Revision: parent.Selector()}, &parent)
	if e != nil {
		return nil, e
	}
	variant, e := types.SelectWindowResourceWithReferences(definition.Resource.Definition, target)
	if e != nil {
		return nil, e
	}
	if e = c.VerifyDependencies(ctx, parent, target, variant); e != nil {
		return nil, e
	}
	raw := variant.DataSources[id]
	if raw == nil {
		return nil, identity.ErrResourceDenied
	}
	var datasource dsproto.DataSource
	if json.Unmarshal(raw, &datasource) != nil || datasource.ID != id {
		return nil, identity.ErrResourceDenied
	}
	if _, e = c.Gateway.Get(ctx, connection, identity.ResourceRef{URI: parent.URI}, &parent); e != nil {
		return nil, e
	}
	if e = c.VerifyDependencies(ctx, parent, target, variant); e != nil {
		return nil, e
	}
	return &datasource, nil
}
