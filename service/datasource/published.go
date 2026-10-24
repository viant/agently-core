package datasource

import (
	"context"
	"fmt"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	"github.com/viant/agently-core/protocol/primitive/observe"
	"github.com/viant/agently-core/runtime/requestctx"
)

// FetchPublished executes an exact datasource descriptor from an admitted,
// immutable top-level window artifact. The caller must first select it from a
// trusted server-side window binding and recheck that admission before release.
// This path does no resource discovery, definition lookup, or row cache read.
// Managing MCP execution remains the business authorization boundary.
func (s *Service) FetchPublished(ctx context.Context, ds *dsproto.DataSource, inputs map[string]interface{}, windowParameters map[string]any) (result *dsproto.FetchResult, fetchErr error) {
	totalTiming := observe.Start(ctx, "datasource.published.total", observe.Labels{Kind: "datasource"})
	defer func() { totalTiming.End(observe.OutcomeFor(fetchErr), observe.CacheBypass) }()
	if s == nil || ds == nil || ds.ID == "" || ds.Backend == nil {
		return nil, fmt.Errorf("published datasource unavailable")
	}
	switch ds.Backend.Kind {
	case dsproto.BackendInline, dsproto.BackendMCPTool:
	default:
		return nil, fmt.Errorf("published datasource backend %q unsupported", ds.Backend.Kind)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.executionContext != nil {
		ctx = s.executionContext(ctx)
	}
	ctx = requestctx.WithWindowParameters(ctx, windowParameters)
	// A published MCP descriptor is already selected by the server-side
	// window binding. The MCP producer alone decides business data access;
	// a consumer authorizer must not veto its authenticated tool result.
	// Inline state has no upstream producer and keeps the local authorizer.
	if s.authorize != nil && ds.Backend.Kind == dsproto.BackendInline {
		if err := s.authorize(ctx, ds.ID, inputs); err != nil {
			return nil, err
		}
	}
	if err := validateExplicitPaging(&ds.DataSource); err != nil {
		return nil, err
	}
	aliases, err := prepareResponseAliases(ds.ResponseAliases)
	if err != nil {
		return nil, err
	}
	normalized := normalizeFilterSemantics(inputs, &ds.DataSource)
	args := expandNestedArgs(mergeArgs(normalized, ds.Backend.Pinned))
	backendTiming := observe.Start(ctx, "datasource.published.backend", observe.Labels{Kind: "datasource"})
	raw, err := s.runBackendRaw(ctx, ds, args)
	backendTiming.End(observe.OutcomeFor(err), observe.CacheBypass)
	if err != nil {
		return nil, err
	}
	rows, dataInfo, metrics := project(raw, &ds.DataSource)
	if err := validatePagingDataInfo(&ds.DataSource, dataInfo, args); err != nil {
		return nil, err
	}
	rows, err = applyResponseAliases(rows, aliases)
	if err != nil {
		return nil, err
	}
	rows, dataInfo = applyPaging(rows, dataInfo, &ds.DataSource, args)
	if rows == nil {
		rows = []map[string]interface{}{}
	}
	return &dsproto.FetchResult{Rows: rows, DataInfo: dataInfo, Metrics: metrics}, nil
}
