package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	dsproto "github.com/viant/agently-core/protocol/datasource"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	"github.com/viant/agently-core/protocol/primitive/observe"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	authctx "github.com/viant/agently-core/service/auth"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/elicitation/refiner"
	oversvc "github.com/viant/agently-core/service/lookup/overlay"
	resourcesvc "github.com/viant/agently-core/service/resource"
)

type reportDatasourceFetcher interface {
	FetchResourceDatasource(context.Context, identity.ResourceRef, string, map[string]interface{}) (*dsproto.FetchResult, error)
}

type reportDatasourceFetchFunc func(context.Context, identity.ResourceRef, string, map[string]interface{}) (*dsproto.FetchResult, error)

func (f reportDatasourceFetchFunc) FetchResourceDatasource(ctx context.Context, owner identity.ResourceRef, id string, inputs map[string]interface{}) (*dsproto.FetchResult, error) {
	return f(ctx, owner, id, inputs)
}

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
	c.configureLookupRefiner()
	if c.primitiveProviders != nil {
		// Gateway instances own their context-aware refinement. Do not replace
		// a legacy runtime's process hook with this instance's overlays.
		return
	}
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

// InlineReportWorkspaceDataSources exposes the current workspace-owned
// datasource allowlist to the generic inline-report policy gate. The
// workspace catalog is shared configuration, but access is fail-closed unless
// the request carries an effective authenticated user. Fence content can
// never add entries to this list.
func (c *backendClient) InlineReportWorkspaceDataSources(ctx context.Context) ([]string, error) {
	if c == nil || c.datasourceStore == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if strings.TrimSpace(authctx.EffectiveUserID(ctx)) == "" {
		return nil, fmt.Errorf("effective authenticated user is required")
	}
	if c.primitiveProviders != nil {
		var result []string
		aliases := map[string]int{}
		query := resourcesvc.CatalogQuery{Limit: 100}
		for {
			page, err := resourcesvc.ListPage[json.RawMessage](ctx, c.primitiveProviders, "datasource", query, true)
			if err != nil {
				return nil, err
			}
			for _, row := range page.Items {
				definition, err := resourcesvc.DecodeDatasourceDefinition(row.Definition)
				if err != nil {
					return nil, err
				}
				result = append(result, row.Resource.URI)
				aliases[definition.ID]++
			}
			if page.Complete {
				break
			}
			query.Cursor = page.NextCursor
		}
		for alias, count := range aliases {
			if count == 1 {
				result = append(result, alias)
			}
		}
		sort.Strings(result)
		return result, nil
	}

	if err := c.refreshDatasourceStack(ctx); err != nil {
		return nil, err
	}
	result := c.datasourceStore.List()
	sort.Strings(result)
	return result, nil
}

// OverlaySvc mirrors DatasourceSvc for the overlay service.
func (c *backendClient) OverlaySvc() *oversvc.Service { return c.overlaySvc }

// FetchDatasource implements DatasourceBackend.
func (c *backendClient) FetchDatasource(ctx context.Context, in *api.FetchDatasourceInput) (output *api.FetchDatasourceOutput, failure error) {
	total := observe.Start(ctx, observe.Phase("datasource.client.fetch"), observe.Labels{Kind: "datasource"})
	defer func() { total.End(observe.OutcomeFor(failure), observe.CacheUnknown) }()
	if c.datasourceSvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if in == nil || strings.TrimSpace(in.ID) == "" {
		return nil, fmt.Errorf("datasource id is required")
	}
	if in.Owner != nil {
		if c.reportDatasources == nil || in.Resource != nil || in.Reference != nil || in.ProviderIdentity != "" || in.Target != nil || in.ResourceProof != nil || in.SelectionBinding != nil {
			return nil, identity.ErrResourceDenied
		}
		result, err := c.reportDatasources.FetchResourceDatasource(ctx, identity.ResourceRef{URI: in.Owner.URI}, in.ID, in.Inputs)
		if err != nil {
			return nil, err
		}
		return toAPIFetchOutput(result), nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var request api.FetchDatasourceInput
	if err = json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	if request.Resource == nil && request.Reference != nil && c.primitiveProviders != nil {
		uri, e := identity.ParseResourceURI(request.Reference.URI)
		if e != nil || uri.Kind != "datasource" || request.ProviderIdentity == "" {
			return nil, identity.ErrResource
		}
		resolution := observe.Start(ctx, observe.Phase("datasource.client.resolve"), observe.Labels{Kind: "datasource"})
		got, e := c.primitiveProviders.GetForProvider(ctx, request.ProviderIdentity, *request.Reference, nil)
		resolution.End(observe.OutcomeFor(e), observe.CacheUnknown)
		if e != nil {
			return nil, e
		}
		definition, e := resourcesvc.DecodeDatasourceDefinition(got.Resource.Definition)
		if e != nil {
			return nil, e
		}
		request.ID = definition.ID
		request.Resource = got.ResolvedResource
		request.ResourceProof = got.ExecutionProof
	}
	if request.Resource == nil && c.primitiveProviders != nil {
		resolution := observe.Start(ctx, observe.Phase("datasource.client.resolve"), observe.Labels{Kind: "datasource"})
		resolved, err := resourcesvc.ResolveDefinition[*dsproto.DataSource](ctx, c.primitiveProviders, "datasource", request.ID, func(ds *dsproto.DataSource) string { return ds.ID }, resourcesvc.DecodeDatasourceDefinition)
		resolution.End(observe.OutcomeFor(err), observe.CacheUnknown)
		if err != nil {
			return nil, err
		}
		request.ID = resolved.Definition.ID
		request.Resource = resolved.ResolvedResource
		request.ResourceProof = resolved.ExecutionProof
	} else if request.Resource == nil {
		if err := c.refreshDatasourceStack(ctx); err != nil {
			return nil, err
		}
	}
	if request.Resource != nil && request.Resource.ProviderIdentity == resourcesvc.InternalProviderIdentity && request.Resource.AuthorityBinding == "public-local-definition" && strings.HasPrefix(request.Resource.URI, "window://workspace/") && !identity.MetadataExpiryValid(request.Resource.ValidUntil, time.Now()) {
		if c.prepareNativePresentationRead == nil {
			return nil, identity.ErrResourceDenied
		}
		request.Resource, request.Target, err = c.prepareNativePresentationRead(ctx, request.Resource, request.Target)
		if err != nil {
			return nil, err
		}
	}
	in = &request
	opts := dssvc.FetchOptions{SelectionBinding: in.SelectionBinding, Resource: in.Resource, Target: in.Target, ResourceProof: in.ResourceProof, WindowParameters: in.WindowParameters}
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
	if c.primitiveProviders != nil {
		resolved, err := resourcesvc.ResolveDefinition[*dsproto.DataSource](ctx, c.primitiveProviders, "datasource", in.ID, func(ds *dsproto.DataSource) string { return ds.ID }, resourcesvc.DecodeDatasourceDefinition)
		if err != nil {
			return err
		}
		return c.datasourceSvc.InvalidateDefinitionCache(requestctx.WithResolvedResource(ctx, *resolved.ResolvedResource), resolved.Definition, in.InputsHash)
	}
	if err := c.refreshDatasourceStack(ctx); err != nil {
		return err
	}
	return c.datasourceSvc.InvalidateCache(ctx, in.ID, in.InputsHash)
}

// ListLookupRegistry implements LookupRegistryBackend.
func (c *backendClient) ListLookupRegistry(ctx context.Context, in *api.ListLookupRegistryInput) (*api.ListLookupRegistryOutput, error) {
	if in == nil || strings.TrimSpace(in.Context) == "" {
		return nil, fmt.Errorf("context query parameter is required")
	}
	if c.primitiveProviders != nil {
		copy := *in
		if copy.Owner == nil {
			if uri, err := identity.ParseResourceURI(copy.Context); err == nil {
				copy.Owner = &identity.ResourceRef{URI: uri.String()}
				copy.Context = uri.Kind + ":" + uri.Name
			}
		}
		var owner *identity.ResolvedResource
		if copy.Owner != nil {
			var err error
			owner, err = c.lookupOwner(ctx, &copy)
			if err != nil {
				return nil, err
			}
		}
		service, err := c.primitiveLookupService(ctx, owner, copy.ProviderIdentity)
		if err != nil {
			return nil, err
		}
		kind, id := splitContext(copy.Context)
		entries, err := service.RegistryScoped(kind, id)
		if err != nil {
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		return &api.ListLookupRegistryOutput{Entries: toAPIRegistryEntries(entries)}, nil
	}
	if c.overlaySvc == nil {
		return nil, ErrDatasourceStackNotConfigured
	}
	if err := c.refreshDatasourceStack(ctx); err != nil {
		return nil, err
	}
	kind, id := splitContext(in.Context)
	return &api.ListLookupRegistryOutput{Entries: toAPIRegistryEntries(c.overlaySvc.Registry(kind, id))}, nil
}

type lookupDefinitionStore struct{ definitions []*loproto.Overlay }

func (s lookupDefinitionStore) List() []*loproto.Overlay { return s.definitions }

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
	out := &api.FetchDatasourceOutput{Rows: r.Rows, DataInfo: r.DataInfo, Metrics: r.Metrics}
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
			DataSourceResource: e.DataSourceResource, PickerResource: e.PickerResource, ProviderIdentity: e.ProviderIdentity,
			Name:       e.Name,
			Title:      e.Title,
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
				Store:        e.Token.Store,
				Display:      e.Token.Display,
				ModelForm:    e.Token.ModelForm,
				QueryInput:   e.Token.QueryInput,
				ResolveInput: e.Token.ResolveInput,
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
