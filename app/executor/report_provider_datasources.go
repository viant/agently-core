package executor

import (
	"context"
	"encoding/json"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func (r *Runtime) providerReportDatasource(ctx context.Context, descriptor *dsproto.DataSource, inputs map[string]interface{}) (json.RawMessage, error) {
	if r.PrimitiveProviders == nil || descriptor == nil {
		return nil, identity.ErrResourceDenied
	}
	parent, ok := requestctx.ResolvedResourceFromContext(ctx)
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	uri, err := identity.ParseResourceURI(parent.URI)
	if err != nil || uri.Kind != "report" {
		return nil, identity.ErrResourceDenied
	}
	child, ok := requestctx.ResourceDependencyFromContext(ctx, descriptor.ID)
	if !ok {
		return nil, identity.ErrResourceDenied
	}
	_, proof, err := r.PrimitiveProviders.ResolveDatasource(ctx, *child)
	if err != nil {
		return nil, err
	}
	return r.PrimitiveProviders.FetchDatasource(ctx, *child, proof, descriptor, inputs)
}
