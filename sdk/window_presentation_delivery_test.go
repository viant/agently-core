package sdk

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	dsproto "github.com/viant/agently-core/protocol/datasource"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/sdk/api"
	dssvc "github.com/viant/agently-core/service/datasource"
	"github.com/viant/forge/backend/types"
	"testing"
	"time"
)

type presentationExecutor func(context.Context, string, map[string]interface{}) (string, error)

func (f presentationExecutor) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return f(ctx, name, args)
}

type presentationCredential struct{}

func TestFetchDatasourceDoesNotRenewNotificationBoundWindow(t *testing.T) {
	pin := &identity.ResolvedResource{URI: "window://workspace/media", ProviderIdentity: "internal", AuthorityBinding: "public-local-definition"}
	target := &types.WindowTarget{Platform: "web", SelectionToken: "original"}
	descriptor := &dsproto.DataSource{ID: "records", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "managing", Method: "read"}}
	prepared, calls := 0, 0
	client := &backendClient{prepareNativePresentationRead: func(context.Context, *identity.ResolvedResource, *types.WindowTarget) (*identity.ResolvedResource, *types.WindowTarget, error) {
		prepared++
		return nil, nil, identity.ErrResourceDenied
	}}
	client.datasourceSvc = dssvc.New(dssvc.Options{DisableCache: true,
		ResolveResource: func(_ context.Context, original identity.ResolvedResource) (*identity.ResolvedResource, error) {
			require.True(t, original.ValidUntil.IsZero())
			return &original, nil
		},
		ResolveDefinition: func(_ context.Context, _ identity.ResolvedResource, held *types.WindowTarget, _ string) (*dsproto.DataSource, error) {
			require.Equal(t, "original", held.SelectionToken)
			return descriptor, nil
		},
		Executor: presentationExecutor(func(_ context.Context, name string, arguments map[string]interface{}) (string, error) {
			calls++
			require.Equal(t, "managing:read", name)
			require.Equal(t, "unchanged", arguments["Name"])
			return `{"data":[{"id":"normal"}]}`, nil
		}),
	})
	result, err := client.FetchDatasource(context.Background(), &api.FetchDatasourceInput{ID: "records", Resource: pin, Target: target, Inputs: map[string]interface{}{"Name": "unchanged"}})
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	require.Equal(t, 1, calls)
	require.Zero(t, prepared)
	require.True(t, pin.ValidUntil.IsZero(), "metadata read cannot manufacture a lease")
	require.Equal(t, "original", target.SelectionToken)
}

func TestFetchDatasourceRefreshesPublicWindowBeforeReadAssociation(t *testing.T) {
	expired := identity.ResolvedResource{URI: "window://workspace/media", ProviderIdentity: "internal", AuthorityBinding: "public-local-definition", ValidUntil: time.Now().Add(-time.Minute)}
	target := &types.WindowTarget{Platform: "web", SelectionToken: "original"}
	prepared, calls := 0, 0
	allowed := true
	descriptor := &dsproto.DataSource{ID: "records", DataSource: types.DataSource{Selectors: &types.Selectors{Data: "data"}}, Backend: &dsproto.Backend{Kind: dsproto.BackendMCPTool, Service: "managing", Method: "read"}}
	client := &backendClient{prepareNativePresentationRead: func(ctx context.Context, pin *identity.ResolvedResource, target *types.WindowTarget) (*identity.ResolvedResource, *types.WindowTarget, error) {
		prepared++
		require.Equal(t, "current-secret-marker", ctx.Value(presentationCredential{}))
		require.Equal(t, "conversation", requestctx.ConversationIDFromContext(ctx))
		require.Equal(t, [32]byte{}, requestctx.ResourceReadAssociation(ctx), "refresh must precede immutable read association")
		if !allowed {
			return nil, nil, identity.ErrResourceDenied
		}
		fresh := *pin
		fresh.ValidUntil = time.Now().Add(time.Minute)
		next := *target
		next.SelectionToken = "fresh"
		return &fresh, &next, nil
	}}
	client.datasourceSvc = dssvc.New(dssvc.Options{DisableCache: true,
		ResolveResource: func(_ context.Context, pin identity.ResolvedResource) (*identity.ResolvedResource, error) {
			if !pin.ValidUntil.After(time.Now()) {
				return nil, identity.ErrResourceDenied
			}
			return &pin, nil
		},
		ResolveDefinition: func(_ context.Context, _ identity.ResolvedResource, target *types.WindowTarget, _ string) (*dsproto.DataSource, error) {
			require.Equal(t, "fresh", target.SelectionToken)
			return descriptor, nil
		},
		Executor: presentationExecutor(func(ctx context.Context, name string, args map[string]interface{}) (string, error) {
			calls++
			require.Equal(t, "managing:read", name)
			require.Equal(t, "current-secret-marker", ctx.Value(presentationCredential{}))
			require.Equal(t, "990001", fmt.Sprint(args["AdvertiserId"]))
			return `{"data":[{"id":"normal"}]}`, nil
		}),
	})
	ctx := requestctx.WithConversationID(context.WithValue(context.Background(), presentationCredential{}, "current-secret-marker"), "conversation")
	input := &api.FetchDatasourceInput{ID: "records", Resource: &expired, Target: target, Inputs: map[string]interface{}{"AdvertiserId": 990001}}
	result, err := client.FetchDatasource(ctx, input)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, prepared)
	require.Equal(t, "original", target.SelectionToken)
	require.True(t, expired.ValidUntil.Before(time.Now()), "old browser metadata must remain immutable")
	allowed = false
	_, err = client.FetchDatasource(ctx, input)
	require.Error(t, err)
	require.Equal(t, 1, calls, "denial must not dispatch data")
	for _, uri := range []string{"report://workspace/media", "datasource://workspace/records", "window://private/media"} {
		private := expired
		private.URI = uri
		prior := prepared
		_, err = client.FetchDatasource(ctx, &api.FetchDatasourceInput{ID: "records", Resource: &private, Target: target})
		require.Error(t, err)
		require.Equal(t, prior, prepared, "nonpresentation business pin must not refresh")
	}
}
