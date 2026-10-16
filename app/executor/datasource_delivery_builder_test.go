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

func TestDatasourceDeliveryBuilderOptionalWiring(t *testing.T) {
	previous := workspace.Root()
	workspace.SetRoot(filepath.Join(t.TempDir(), "workspace"))
	defer workspace.SetRoot(previous)
	calls := 0
	preparer := func(ctx context.Context, in dssvc.OriginalDelivery) (context.Context, *dssvc.PreparedDelivery, error) {
		calls++
		return ctx, &dssvc.PreparedDelivery{Resource: in.Resource, Target: in.Target, ResourceProof: in.ResourceProof}, nil
	}
	for _, mode := range []string{"default", "configured", "cleared"} {
		t.Run(mode, func(t *testing.T) {
			builder := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{})
			if mode != "default" {
				builder.WithNewDatasourceDeliveryPreparer(preparer)
			}
			if mode == "cleared" {
				builder.WithNewDatasourceDeliveryPreparer(nil)
			}
			runtime, err := builder.Build(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close(context.Background())
			if calls != 0 {
				t.Fatal("preparer invoked at startup")
			}
			if mode != "configured" {
				if runtime.DatasourceNewDeliveryPreparer != nil {
					t.Fatal("nil default not preserved")
				}
				return
			}
			if runtime.DatasourceNewDeliveryPreparer == nil {
				t.Fatal("trusted preparer lost")
			}
			pin := &identity.ResolvedResource{URI: "window://arbitrary/view"}
			_, got, err := runtime.DatasourceNewDeliveryPreparer(context.Background(), dssvc.OriginalDelivery{Resource: pin})
			if err != nil || got == nil || got.Resource != pin || calls != 1 {
				t.Fatal("callback replaced", err)
			}
			calls = 0
		})
	}
}

func TestDatasourceDeliveryPreparerPreservesIncompleteBoundaryValidation(t *testing.T) {
	calls := 0
	preparer := func(context.Context, dssvc.OriginalDelivery) (context.Context, *dssvc.PreparedDelivery, error) {
		calls++
		return nil, nil, nil
	}
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
		runtime, err := executor.NewBuilder().WithNewDatasourceDeliveryPreparer(preparer).WithPinnedDatasourceDefinitions(revalidate, resolve, authorize).Build(context.Background())
		if runtime != nil || err == nil || !strings.Contains(err.Error(), "pinned datasource definitions") || calls != 0 {
			t.Fatalf("mask %d validation/startup changed: %v calls=%d", mask, err, calls)
		}
	}
}
