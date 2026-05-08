package sdk

import (
	"context"
	"fmt"

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
	c.datasourceStore = dsStore

	// Load overlays.
	overlayStore := oversvc.NewMemoryStore()
	c.overlayStore = overlayStore

	if err := c.refreshDatasourceStack(ctx); err != nil {
		return err
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

func (c *backendClient) refreshDatasourceStack(ctx context.Context) error {
	if c == nil || c.store == nil {
		return nil
	}
	if err := c.refreshForgeDatasources(ctx); err != nil {
		return err
	}
	if err := c.refreshForgeLookups(ctx); err != nil {
		return err
	}
	return nil
}

func (c *backendClient) refreshForgeDatasources(ctx context.Context) error {
	if c == nil || c.store == nil || c.datasourceStore == nil {
		return nil
	}
	repo := forgedatasource.NewWithStore(c.store)
	names, err := repo.List(ctx)
	if err != nil {
		return fmt.Errorf("list forge datasources: %w", err)
	}
	loaded := make([]*dsproto.DataSource, 0, len(names))
	for _, name := range names {
		v, err := repo.Load(ctx, name)
		if err != nil {
			return fmt.Errorf("load forge datasource %q: %w", name, err)
		}
		if v == nil {
			continue
		}
		if v.ID == "" {
			v.ID = name
		}
		loaded = append(loaded, v)
	}
	c.datasourceStore.Replace(loaded)
	return nil
}

func (c *backendClient) refreshForgeLookups(ctx context.Context) error {
	if c == nil || c.store == nil || c.overlayStore == nil {
		return nil
	}
	repo := forgelookup.NewWithStore(c.store)
	names, err := repo.List(ctx)
	if err != nil {
		return fmt.Errorf("list forge lookups: %w", err)
	}
	loaded := make([]*loproto.Overlay, 0, len(names))
	for _, name := range names {
		v, err := repo.Load(ctx, name)
		if err != nil {
			return fmt.Errorf("load forge lookup %q: %w", name, err)
		}
		if v == nil {
			continue
		}
		if v.ID == "" {
			v.ID = name
		}
		loaded = append(loaded, v)
	}
	c.overlayStore.Replace(loaded)
	return nil
}
