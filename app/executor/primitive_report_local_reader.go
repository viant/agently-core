package executor

import (
	"context"
	"encoding/json"

	identity "github.com/viant/agently-core/protocol/resource"
	reporting "github.com/viant/agently-core/service/reporting"
	catalog "github.com/viant/agently-core/service/reporting/catalog"
	resources "github.com/viant/agently-core/service/resource"
)

// Legacy local resources without MCP registration use trusted inventory
// ownership before action policy. An action denial never selects a new source.
type primitiveReportLocalReader struct {
	local     *catalog.ReportCatalogService
	resolve   reporting.ResourceResolver
	remote    reporting.ProviderResourceReader
	operation string
}

func (r *primitiveReportLocalReader) owns(ctx context.Context, uri string) (bool, error) {
	actor, err := r.local.CurrentActor(ctx)
	if err != nil {
		return false, err
	}
	count := 0
	for _, inventory := range r.local.Inventories {
		if inventory == nil {
			return false, identity.ErrResourceDenied
		}
		candidates, err := inventory.ListReportResources(ctx, actor)
		if err != nil {
			return false, err
		}
		for _, candidate := range candidates {
			parsed, err := identity.ParseResourceURI(candidate.URI)
			if err != nil || parsed.Kind != "report" {
				return false, identity.ErrResourceDenied
			}
			if candidate.URI == uri {
				count++
			}
		}
	}
	if count > 1 {
		return false, resources.ErrCollision
	}
	return count == 1, nil
}
func (r *primitiveReportLocalReader) Resolve(ctx context.Context, ref identity.ResourceRef) (*identity.ResolvedResource, error) {
	local, err := r.owns(ctx, ref.URI)
	if err != nil {
		return nil, err
	}
	if !local {
		return r.remote.Resolve(ctx, ref)
	}
	reader, err := r.resolve(ctx, r.operation)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, identity.ErrResourceDenied
	}
	return reader.Resolve(ctx, ref)
}
func (r *primitiveReportLocalReader) ReadResolved(ctx context.Context, pin identity.ResolvedResource) (json.RawMessage, *identity.ResolvedResource, error) {
	local, err := r.owns(ctx, pin.URI)
	if err != nil {
		return nil, nil, err
	}
	if !local {
		return r.remote.ReadResolved(ctx, pin)
	}
	reader, err := r.resolve(ctx, r.operation)
	if err != nil {
		return nil, nil, err
	}
	if reader == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	return reader.ReadResolved(ctx, pin)
}
func (r *primitiveReportLocalReader) ResolveForProvider(ctx context.Context, ref identity.ResourceRef, provider string) (*identity.ResolvedResource, error) {
	local, err := r.owns(ctx, ref.URI)
	if err != nil {
		return nil, err
	}
	if !local {
		return r.remote.ResolveForProvider(ctx, ref, provider)
	}
	reader, err := r.resolve(ctx, r.operation)
	if err != nil {
		return nil, err
	}
	if reader == nil || reader.ProviderIdentity != provider {
		return nil, identity.ErrResourceDenied
	}
	return reader.Resolve(ctx, ref)
}
