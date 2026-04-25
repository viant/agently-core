package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/elicitation/refiner"
	oversvc "github.com/viant/agently-core/service/lookup/overlay"
)

// SetDatasourceStack wires the datasource + overlay services into the
// embedded backend and installs the elicitation refiner hook.
//
// Passing nil for either argument removes that piece of functionality —
// the HTTP handlers will revert to 501 for the unconfigured routes, and
// the elicitation refiner will no longer call the overlay translator.
//
// This is the production wiring site that previously had to be reached
// through an external hook. After this is called, both the HTTP routes
// and the embedded client path serve the feature end-to-end, and
// service/elicitation/refiner.Refine runs overlay.Apply on every schema.
func (c *backendClient) SetDatasourceStack(ds *dssvc.Service, overlay *oversvc.Service) {
	if c == nil {
		return
	}
	c.datasourceSvc = ds
	c.overlaySvc = overlay
	if overlay != nil {
		// Install a wildcard hook so overlays targeted at any kind
		// (template, tool, elicitation, …) all get a chance to fire, and
		// library overlays with no Target still apply schema-wide. Authors
		// that need strictly kind-scoped matching can override by calling
		// refiner.SetOverlayHook(...) with a context-specific factory
		// after this call.
		refiner.SetOverlayHook(overlay.NewWildcardHook())
	} else {
		refiner.SetOverlayHook(nil)
	}
}

// DatasourceSvc is exposed so tests and runtime glue can reuse the
// configured instance (e.g. for the scheduler, for direct Go callers).
func (c *backendClient) DatasourceSvc() *dssvc.Service { return c.datasourceSvc }

// OverlaySvc mirrors DatasourceSvc for the overlay service.
func (c *backendClient) OverlaySvc() *oversvc.Service { return c.overlaySvc }

// FetchDatasource implements DatasourceBackend.
func (c *backendClient) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (*api.FetchDatasourceOutput, error) {
	if c.datasourceSvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if in == nil || strings.TrimSpace(in.ID) == "" {
		return nil, fmt.Errorf("datasource id is required")
	}
	opts := dssvc.FetchOptions{}
	if in.Cache != nil {
		opts.BypassCache = in.Cache.BypassCache
		opts.WriteThrough = in.Cache.WriteThrough
	}
	res, err := c.datasourceSvc.Fetch(ctx, in.ID, in.Inputs, opts)
	if err != nil {
		return nil, err
	}
	return toAPIFetchOutput(res), nil
}

// InvalidateDatasourceCache implements DatasourceBackend.
func (c *backendClient) InvalidateDatasourceCache(ctx context.Context, in *api.InvalidateDatasourceCacheInput) error {
	if c.datasourceSvc == nil {
		return ErrDatasourceStackNotConfigured
	}
	if in == nil || strings.TrimSpace(in.ID) == "" {
		return fmt.Errorf("datasource id is required")
	}
	return c.datasourceSvc.InvalidateCache(ctx, in.ID, in.InputsHash)
}

// ListLookupRegistry implements LookupRegistryBackend.
func (c *backendClient) ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	if c.overlaySvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if in == nil || strings.TrimSpace(in.Context) == "" {
		return nil, fmt.Errorf("context query parameter is required")
	}
	kind, id := splitContext(in.Context)
	entries := c.overlaySvc.Registry(kind, id)
	return &api.ListLookupRegistryOutput{Entries: toAPIRegistryEntries(entries)}, nil
}

func splitContext(s string) (kind, id string) {
	i := strings.Index(s, ":")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func toAPIFetchOutput(r *dsproto.FetchResult) *api.FetchDatasourceOutput {
	if r == nil {
		return &api.FetchDatasourceOutput{}
	}
	out := &api.FetchDatasourceOutput{Rows: r.Rows, DataInfo: r.DataInfo}
	if r.Cache != nil {
		out.Cache = &api.DatasourceCacheMeta{
			Hit:        r.Cache.Hit,
			Stale:      r.Cache.Stale,
			FetchedAt:  r.Cache.FetchedAt.UTC().Format(time.RFC3339Nano),
			TTLSeconds: r.Cache.TTLSeconds,
		}
	}
	return out
}

func toAPIRegistryEntries(in []loproto.RegistryEntry) []api.LookupRegistryEntry {
	out := make([]api.LookupRegistryEntry, 0, len(in))
	for _, e := range in {
		entry := api.LookupRegistryEntry{
			Name:       e.Name,
			DataSource: e.DataSource,
			DialogId:   e.DialogId,
			WindowId:   e.WindowId,
			Trigger:    e.Trigger,
			Required:   e.Required,
			Display:    e.Display,
			Inputs:     toAPIParams(e.Inputs),
			Outputs:    toAPIParams(e.Outputs),
		}
		if e.Token != nil {
			entry.Token = &api.TokenFormat{
				Store:     e.Token.Store,
				Display:   e.Token.Display,
				ModelForm: e.Token.ModelForm,
			}
		}
		out = append(out, entry)
	}
	return out
}

func toAPIParams(ps []loproto.Parameter) []api.LookupParameter {
	if len(ps) == 0 {
		return nil
	}
	out := make([]api.LookupParameter, 0, len(ps))
	for _, p := range ps {
		out = append(out, api.LookupParameter{
			From:     p.From,
			To:       p.To,
			Name:     p.Name,
			Location: p.Location,
		})
	}
	return out
}
