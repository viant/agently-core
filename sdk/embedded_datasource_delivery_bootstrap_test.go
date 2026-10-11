package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/viant/agently-core/app/executor"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/agently-core/service/elicitation/refiner"
	"github.com/viant/forge/backend/types"
)

func TestBootstrapDatasourceDeliveryPreparerWiresOnlyFetch(t *testing.T) {
	defer refiner.SetOverlayHook(nil)
	calls := 0
	runtime := &executor.Runtime{DatasourceNewDeliveryPreparer: func(context.Context, dssvc.OriginalDelivery) (context.Context, *dssvc.PreparedDelivery, error) {
		calls++
		return nil, nil, identity.ErrResourceDenied
	}}
	client := &backendClient{}
	if err := client.bootstrapDatasourceStack(runtime); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("bootstrap invoked preparer")
	}
	original := &identity.ResolvedResource{ProviderIdentity: "owner", URI: "window://arbitrary/view", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}, AuthorityBinding: "original", ValidUntil: time.Now().Add(time.Minute)}
	input := &api.FetchDatasourceInput{ID: "rows", Resource: original, SelectionBinding: &identity.SelectionBinding{Token: "original"}}
	if _, err := client.FetchDatasource(context.Background(), input); !errors.Is(err, identity.ErrResourceDenied) || calls != 1 {
		t.Fatalf("trusted callback lost: %v calls=%d", err, calls)
	}
	// Rebootstrap without a callback restores the ordinary default. A signed
	// binding cannot silently bypass an unsupported host preparation contract.
	if err := client.bootstrapDatasourceStack(&executor.Runtime{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchDatasource(context.Background(), input); !errors.Is(err, identity.ErrResourceDenied) || calls != 1 {
		t.Fatalf("disabled host did not fail closed: %v calls=%d", err, calls)
	}
	client.datasourceStore.Put(&dsproto.DataSource{ID: "inline", Backend: &dsproto.Backend{Kind: dsproto.BackendInline, Rows: []map[string]interface{}{{"id": "opaque"}}}})
	output, err := client.FetchDatasource(context.Background(), &api.FetchDatasourceInput{ID: "inline"})
	if err != nil || output == nil || len(output.Rows) != 1 || output.Rows[0]["id"] != "opaque" || calls != 1 {
		t.Fatalf("ordinary fetch changed: %+v %v calls=%d", output, err, calls)
	}
}

func TestBootstrapDatasourceDeliveryPreparerRetainsValidArtifact(t *testing.T) {
	defer refiner.SetOverlayHook(nil)
	original := identity.ResolvedResource{ProviderIdentity: "owner", URI: "window://arbitrary/view", ResourceCandidate: identity.ResourceCandidate{Kind: identity.WorkingCandidate, ContentFingerprint: identity.ContentFingerprint([]byte(`{}`))}, AuthorityBinding: "current", ValidUntil: time.Now().Add(time.Minute)}
	descriptor := &dsproto.DataSource{ID: "rows", Backend: &dsproto.Backend{Kind: dsproto.BackendInline, Rows: []map[string]interface{}{{"id": "opaque"}}}}
	preparation, revalidation := 0, 0
	runtime := &executor.Runtime{DatasourceDisableCache: true,
		DatasourceNewDeliveryPreparer: func(ctx context.Context, in dssvc.OriginalDelivery) (context.Context, *dssvc.PreparedDelivery, error) {
			preparation++
			return ctx, &dssvc.PreparedDelivery{Resource: in.Resource}, nil
		},
		DatasourceResourceRevalidator: func(_ context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
			revalidation++
			samePin := pin
			samePin.ValidUntil = original.ValidUntil
			if samePin != original || !pin.ValidUntil.Equal(original.ValidUntil) {
				return nil, identity.ErrResourceDenied
			}
			return &pin, nil
		},
		DatasourceDefinitionResolver: func(context.Context, identity.ResolvedResource, *types.WindowTarget, string) (*dsproto.DataSource, error) {
			return descriptor, nil
		},
	}
	client := &backendClient{}
	if err := client.bootstrapDatasourceStack(runtime); err != nil {
		t.Fatal(err)
	}
	if preparation != 0 || revalidation != 0 {
		t.Fatal("callbacks evaluated at startup")
	}
	input := &api.FetchDatasourceInput{ID: "rows", Resource: &original, SelectionBinding: &identity.SelectionBinding{Token: "original"}}
	before := original
	output, err := client.FetchDatasource(context.Background(), input)
	if err != nil || output == nil || len(output.Rows) != 1 || preparation != 1 || revalidation != 1 || original != before {
		t.Fatalf("valid artifact replaced or callback lost: %+v %v prep=%d revalidation=%d", output, err, preparation, revalidation)
	}
}
