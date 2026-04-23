package sdk

import (
	"context"

	"github.com/viant/agently-core/app/executor"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	loproto "github.com/viant/agently-core/protocol/lookup/overlay"
	dssvc "github.com/viant/agently-core/service/datasource"
	dsadapter "github.com/viant/agently-core/service/datasource/adapter"
	oversvc "github.com/viant/agently-core/service/lookup/overlay"
	"github.com/viant/agently-core/workspace/repository/forgedatasource"
	"github.com/viant/agently-core/workspace/repository/forgelookup"
)

// bootstrapDatasourceStack is called from newBackendFromRuntime. It loads any
// extension/forge/datasources/ and extension/forge/lookups/ YAML from the
// runtime's workspace store, constructs the two services, and calls
// SetDatasourceStack — which also installs the elicitation refiner hook.
//
// Errors from optional loads are swallowed: a workspace without the new kinds
// is not an error, it just yields an empty stack.
func (c *backendClient) bootstrapDatasourceStack(rt *executor.Runtime) error {
	if c == nil || rt == nil {
		return nil
	}

	ctx := context.Background()

	// Load datasources.
	dsStore := dssvc.NewMemoryStore()
	if rt.Store != nil {
		ds := forgedatasource.NewWithStore(rt.Store)
		names, err := ds.List(ctx)
		if err == nil {
			for _, name := range names {
				v, err := ds.Load(ctx, name)
				if err != nil || v == nil {
					continue
				}
				// Ensure ID is populated when absent — fall back to the
				// resource's file name so URL path lookups resolve.
				if v.ID == "" {
					v.ID = name
				}
				dsStore.Put(v)
			}
		}
	}

	// Load overlays.
	overlayStore := oversvc.NewMemoryStore()
	if rt.Store != nil {
		ol := forgelookup.NewWithStore(rt.Store)
		names, err := ol.List(ctx)
		if err == nil {
			loaded := make([]*loproto.Overlay, 0, len(names))
			for _, name := range names {
				v, err := ol.Load(ctx, name)
				if err != nil || v == nil {
					continue
				}
				if v.ID == "" {
					v.ID = name
				}
				loaded = append(loaded, v)
			}
			overlayStore.Replace(loaded)
		}
	}

	// Build the datasource service. Executor stays nil when the runtime
	// has no tool registry — inline / feed_ref backends continue to work;
	// mcp_tool backends return a clear error at Fetch time.
	opts := dssvc.Options{Store: dsStore}
	if rt.Registry != nil {
		if reg, ok := rt.Registry.(dsadapter.ToolRegistry); ok {
			opts.Executor = dsadapter.FromRegistry(reg)
		}
	}
	dsService := dssvc.New(opts)

	// Build the overlay service.
	overlayService := oversvc.New(overlayStore)

	// Wire both + install refiner hook.
	c.SetDatasourceStack(dsService, overlayService)

	// Compile-time assertions that bootstrap didn't drop a target kind.
	_ = []interface{}{
		dsproto.BackendMCPTool, loproto.ModePartial,
	}
	return nil
}
