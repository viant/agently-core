package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/policy"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type trackedAliasBridge struct {
	*bridge.Service
	calls   map[string]int
	failure error
}

func (b *trackedAliasBridge) WindowResourceReference(ctx context.Context, key string) (identity.ResourceRef, error) {
	b.calls[key]++
	if b.failure != nil {
		return identity.ResourceRef{}, b.failure
	}
	return b.Service.WindowResourceReference(ctx, key)
}

func TestLayoutPreservesConfiguredAliasesThroughAuthenticatedMCPBridge(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "awitas"})
	actor := identity.VerifiedActor{Subject: "awitas", Issuer: "fixture", TenantID: "tenant", AccountID: "account", IdentityRevision: "v1", ValidUntil: time.Now().Add(time.Hour)}
	resolveActor := func(ctx context.Context) (identity.VerifiedActor, error) {
		if authctx.EffectiveUserID(ctx) != actor.Subject {
			return identity.VerifiedActor{}, identity.ErrResourceDenied
		}
		return actor, nil
	}
	verify := func(ctx context.Context, a identity.VerifiedActor) error {
		if a.Subject != actor.Subject || authctx.EffectiveUserID(ctx) != a.Subject {
			return identity.ErrResourceDenied
		}
		return nil
	}
	variant := types.WindowResourceVariant{Window: &types.Window{View: types.View{Content: &types.Container{ID: "original-content"}}}, DataSources: map[string]json.RawMessage{}}
	fingerprint, err := types.WindowVariantFingerprint(variant)
	require.NoError(t, err)
	raw, err := json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Target: types.WindowTarget{}, Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	require.NoError(t, err)
	var denyCampaign, denyAll atomic.Bool
	values := []string{"window://workspace/advertiserList", "window://workspace/campaignList", "window://system/schedule", "window://external/unknown"}
	bindings := make([]resources.LocalResourceBinding, 0, len(values))
	for _, value := range values {
		uri, err := identity.ParseResourceURI(value)
		require.NoError(t, err)
		bindings = append(bindings, resources.LocalResourceBinding{URI: value, Title: uri.Name, FormatVersion: 2, Resolver: func(_ context.Context, a identity.VerifiedActor, _ string) (*identity.ResourceResolver, error) {
			return &identity.ResourceResolver{ProviderIdentity: "internal", Source: &identity.LocalResource{URI: uri, Load: func(context.Context) (json.RawMessage, error) { return raw, nil }}, Policy: capturedMenuPolicy{actor: a}}, nil
		}})
	}
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "internal", Actor: resolveActor, Verify: verify, Authorize: func(_ context.Context, _ identity.VerifiedActor, uri identity.ResourceURI, _ string) error {
		if uri.Kind == "window" && (denyAll.Load() || uri.Name == "campaignList" && denyCampaign.Load()) {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"window": resources.ValidateWindowBundle}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	require.NoError(t, mgr.RegisterLocal(ctx, "internal", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "internal"}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	gateway := resources.NewGateway(mgr, resolveActor, verify, "aggregate")
	t.Cleanup(gateway.Close)
	proof, err := types.NewWindowTargetHMAC([]byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	catalog := &resources.WindowCatalog{TargetProof: proof, Gateway: gateway, DefaultNamespace: "workspace", DefaultProviderIdentity: "internal", Aliases: map[string]string{"schedule": "window://system/schedule"}}
	uiBridge := bridge.NewService(&bridge.Config{WindowDefinitions: catalog})
	require.True(t, uiBridge.UsesWindowResourceResolution())
	tracked := &trackedAliasBridge{Service: uiBridge, calls: map[string]int{}}
	list, err := uiBridge.WindowDefinitionsList(ctx, &bridge.WindowDefinitionListInput{})
	require.NoError(t, err)
	require.Len(t, list.Windows, 4)
	for _, summary := range list.Windows {
		require.NotEqual(t, summary.Name, summary.WindowID, "real gateway emits encoded locators, not invented bare IDs")
	}
	layout := &Layout{Applications: []LayoutApplication{{ID: "steward", Title: "Workspace", Menus: []LayoutMenu{
		{ID: "advertisers", Title: "Advertisers", Action: &LayoutAction{Type: "window", WindowKey: "advertiserList", Parameters: map[string]interface{}{"original": "parameter"}}},
		{ID: "campaigns", Title: "Campaigns", Action: &LayoutAction{Type: "window", WindowKey: "campaignList", RefreshDataSources: []string{"campaigns"}}},
		{ID: "foreign", Title: "Unknown bare external", Action: &LayoutAction{Type: "window", WindowKey: "unknown"}},
	}}}, Topbar: &LayoutTopbar{Actions: []LayoutMenu{{ID: "automation", Title: "Automation", Action: &LayoutAction{Type: "window", WindowKey: "schedule", RefreshDataSources: []string{"schedules"}}}}}}
	got, err := filterLayoutContextWithRuntimes(ctx, layout, nil, nil, tracked)
	require.NoError(t, err)
	require.Len(t, got.Applications, 1)
	require.Len(t, got.Applications[0].Menus, 2)
	require.Equal(t, layout.Applications[0].Menus[:2], got.Applications[0].Menus, "all original action parameters/title/refresh lists remain unchanged")
	require.Equal(t, layout.Topbar.Actions, got.Topbar.Actions)
	require.Equal(t, map[string]int{"advertiserList": 1, "campaignList": 1, "schedule": 1, "unknown": 1}, tracked.calls)
	aliases, err := catalogWindowIDs(ctx, tracked)
	require.NoError(t, err)
	tracked.calls = map[string]int{}
	require.NoError(t, addVisibleLayoutAliases(ctx, tracked, aliases, []policy.Candidate{{ID: "unknown"}, {ID: "unknown"}, {ID: "advertiserList"}, {ID: "advertiserList"}}))
	require.Equal(t, map[string]int{"unknown": 1, "advertiserList": 1}, tracked.calls, "one routing lookup per key even when multiple branches reference it")
	tracked.failure = errors.New("routing source unavailable")
	_, err = filterLayoutContextWithRuntimes(ctx, layout, nil, nil, tracked)
	require.ErrorIs(t, err, tracked.failure, "routing outage must not publish guessed aliases")
	tracked.failure = nil
	denyCampaign.Store(true)
	got, err = filterLayoutContextWithRuntimes(ctx, layout, nil, nil, tracked)
	require.NoError(t, err)
	require.Len(t, got.Applications, 1)
	require.Len(t, got.Applications[0].Menus, 1)
	require.Equal(t, "advertisers", got.Applications[0].Menus[0].ID)
	require.Equal(t, layout.Topbar.Actions, got.Topbar.Actions)
	denyAll.Store(true)
	got, err = filterLayoutContextWithRuntimes(ctx, layout, nil, nil, tracked)
	require.NoError(t, err)
	require.Empty(t, got.Applications)
	require.Empty(t, got.Topbar.Actions, "empty managing catalog publishes no fallback actions")
	_, err = filterLayoutContextWithRuntimes(context.Background(), layout, nil, nil, tracked)
	require.Error(t, err, "identity must reach the managing catalog")
}
