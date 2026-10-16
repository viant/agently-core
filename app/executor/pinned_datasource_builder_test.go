package executor_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/app/executor"
	ds "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/forge/backend/types"
)

func TestPinnedDatasourceBuilderRequiresCompleteTrustedBoundaryBeforeStartup(t *testing.T) {
	for mask := 1; mask < 7; mask++ {
		var revalidate dssvc.ResourceRevalidator
		var resolve dssvc.ResourceDefinitionResolver
		var authorize dssvc.DefinitionAuthorizer
		if mask&1 != 0 {
			revalidate = func(context.Context, identity.ResolvedResource) (*identity.ResolvedResource, error) { return nil, nil }
		}
		if mask&2 != 0 {
			resolve = func(context.Context, identity.ResolvedResource, *types.WindowTarget, string) (*ds.DataSource, error) {
				return nil, nil
			}
		}
		if mask&4 != 0 {
			authorize = func(context.Context, *ds.DataSource, map[string]interface{}) error { return nil }
		}
		// No finders/stores are installed: incomplete authority must be rejected
		// before loading a workspace or allocating native runtime resources.
		runtime, err := executor.NewBuilder().WithPinnedDatasourceDefinitions(revalidate, resolve, authorize).Build(context.Background())
		if runtime != nil || err == nil || !strings.Contains(err.Error(), "pinned datasource definitions") {
			t.Fatalf("mask %d: partial boundary accepted or startup preceded validation: %v", mask, err)
		}
	}
}

func TestPinnedDatasourceBuilderRetainsHostCallbacks(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(filepath.Join(root, "workspace"))
	defer workspace.SetRoot(previous)
	calls := 0
	revalidate := func(_ context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
		calls++
		return &pin, nil
	}
	resolve := func(_ context.Context, _ identity.ResolvedResource, _ *types.WindowTarget, id string) (*ds.DataSource, error) {
		calls++
		return &ds.DataSource{ID: id}, nil
	}
	authorize := func(context.Context, *ds.DataSource, map[string]interface{}) error {
		calls++
		return identity.ErrResourceDenied
	}
	runtime, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithPinnedDatasourceDefinitions(revalidate, resolve, authorize).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	if calls != 0 {
		t.Fatal("authority callbacks evaluated at startup")
	}
	pin := identity.ResolvedResource{URI: "datasource://arbitrary/catalog/records"}
	got, err := runtime.DatasourceResourceRevalidator(context.Background(), pin)
	if err != nil || got == nil || *got != pin {
		t.Fatal("host resource boundary lost", err)
	}
	definition, err := runtime.DatasourceDefinitionResolver(context.Background(), pin, nil, "records")
	if err != nil || definition == nil || definition.ID != "records" {
		t.Fatal("host definition boundary lost", err)
	}
	if runtime.DatasourceDefinitionAuthorizer(context.Background(), definition, nil) != identity.ErrResourceDenied {
		t.Fatal("host execution authorization was replaced")
	}
	if calls != 3 {
		t.Fatal("unexpected callback execution count", calls)
	}
}
