package executor

import (
	"context"
	"encoding/base64"
	"errors"
	"sort"

	identity "github.com/viant/agently-core/protocol/resource"
	catalog "github.com/viant/agently-core/service/reporting/catalog"
	resources "github.com/viant/agently-core/service/resource"
)

// This adapter retains unregistered legacy local ownership while projecting
// gateway reports. Both catalogs use the existing canonical-URI cursor format.
type primitiveReportLocalCatalog struct {
	local  *catalog.ReportCatalogService
	remote *resources.ReportGatewayCatalog
	owner  *primitiveReportLocalReader
}

func (c *primitiveReportLocalCatalog) CurrentActor(ctx context.Context) (identity.VerifiedActor, error) {
	return c.remote.CurrentActor(ctx)
}
func (c *primitiveReportLocalCatalog) MetadataRead(ctx context.Context) (context.Context, func() error, error) {
	localCtx, localFinish, err := c.local.MetadataRead(ctx)
	if err != nil {
		return nil, nil, err
	}
	if localCtx == nil || localFinish == nil {
		return nil, nil, identity.ErrResourceDenied
	}
	remoteCtx, remoteFinish, err := c.remote.MetadataRead(localCtx)
	if err != nil {
		_ = localFinish()
		return nil, nil, err
	}
	if remoteCtx == nil || remoteFinish == nil {
		_ = localFinish()
		return nil, nil, identity.ErrResourceDenied
	}
	return remoteCtx, func() error { return errors.Join(remoteFinish(), localFinish()) }, nil
}
func (c *primitiveReportLocalCatalog) Read(ctx context.Context, ref identity.ResourceRef, pin *identity.ResolvedResource) ([]byte, *identity.ResolvedResource, error) {
	local, err := c.owner.owns(ctx, ref.URI)
	if err != nil {
		return nil, nil, err
	}
	if local {
		return c.local.Read(ctx, ref, pin)
	}
	return c.remote.Read(ctx, ref, pin)
}
func (c *primitiveReportLocalCatalog) List(ctx context.Context, query catalog.ReportCatalogQuery) (*catalog.ReportCatalogResult, error) {
	limit := query.Limit
	if limit == 0 {
		limit = 1000
	}
	if limit < 0 || limit > 1000 {
		return nil, identity.ErrResource
	}
	query.Limit = limit + 1
	if query.Limit > 1000 {
		query.Limit = 1000
	}
	left, err := c.local.List(ctx, query)
	if err != nil {
		return nil, err
	}
	right, err := c.remote.List(ctx, query)
	if err != nil {
		return nil, err
	}
	if left == nil || right == nil {
		return nil, identity.ErrResourceDenied
	}
	combined := append([]catalog.ReportCatalogEntry(nil), left.Reports...)
	for _, entry := range right.Reports {
		local, err := c.owner.owns(ctx, entry.URI)
		if err != nil {
			return nil, err
		}
		if local {
			return nil, resources.ErrCollision
		}
		combined = append(combined, entry)
	}
	sort.Slice(combined, func(i, j int) bool { return combined[i].URI < combined[j].URI })
	result := &catalog.ReportCatalogResult{Reports: combined}
	if len(combined) > limit {
		result.Reports = combined[:limit]
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Reports[limit-1].URI))
	}
	if result.NextCursor == "" && len(result.Reports) > 0 && (left.NextCursor != "" || right.NextCursor != "") {
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(result.Reports[len(result.Reports)-1].URI))
	}
	return result, nil
}
