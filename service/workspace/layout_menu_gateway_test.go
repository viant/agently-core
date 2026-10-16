package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	producer "github.com/viant/agently-core/service/resource/provider"
	"github.com/viant/agently-core/service/ui/layoutgate"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type capturedMenuPolicy struct{ actor identity.VerifiedActor }

func (p capturedMenuPolicy) SelectRevision(_ context.Context, ref identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(candidates) != 1 || (ref.Revision != "" && ref.Revision != identity.WorkingCandidate) {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "original-menu", ValidUntil: p.actor.ValidUntil}, nil
}

func TestGatewayLayoutWarmReadsCurrentGateAndPinnedProjection(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := filepath.Join(root, "ui/layout.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
	raw := []byte(`version: 1
id: original
windowProviders: [{id: operations-ui, type: mcp, serverRef: operations, catalogTool: list_ui_windows, windowTool: get_ui_window}]
applications:
  - id: public
    title: Public
    menus: [{id: public-action, title: Public, action: {type: window, provider: operations-ui, windowKey: same}}]
  - id: protected
    title: Protected
    authorization: {resourceType: application}
    visibleWhen: {source: authorization, field: principal.features, contains: OPERATIONS_UI}
    menus: [{id: protected-action, title: Protected, action: {type: window, provider: operations-ui, windowKey: same}}]
`)
	require.NoError(t, os.WriteFile(file, raw, 0600))
	handler := NewMetadataHandler(nil, nil, "test")
	facts := &mutableLayoutFacts{features: []string{"OPERATIONS_UI"}}
	runtime := &permittedview.Runtime{Resolver: facts}
	handler.SetPermittedRuntime(runtime)
	def, err := resources.SnapshotLayoutMenuDefinition("ui/layout.yaml", "navigation://team/layout")
	require.NoError(t, err)
	loads := 0
	var snapshot *resources.NativeAssetSnapshot
	uri, _ := identity.ParseResourceURI(def.URI)
	def.Load = resources.NativeLayoutMenuLoaderWithCapture(func(bytes []byte) error {
		loads++
		return handler.SetCapturedLayout(root, "ui/layout.yaml", bytes, func(ctx context.Context) error { _, err := snapshot.Candidates(ctx, uri); return err })
	})
	snapshot, err = resources.NewNativeAssetSnapshot(ctx, root, []resources.NativeSnapshotDefinition{def}, resources.NativeSnapshotOptions{})
	require.NoError(t, err)
	actor := identity.VerifiedActor{Issuer: "https://fixture.invalid", Subject: "owner", TenantID: "team", AccountID: "account", IdentityRevision: "current", ValidUntil: time.Now().Add(time.Minute)}
	resolve := func(context.Context) (identity.VerifiedActor, error) { return actor, nil }
	verify := func(_ context.Context, expected identity.VerifiedActor) error {
		if expected != actor || !actor.Valid(time.Now()) {
			return identity.ErrResourceDenied
		}
		return nil
	}
	bindings, err := snapshot.Bindings("menu-provider", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return capturedMenuPolicy{a}, nil
	})
	require.NoError(t, err)
	changes := &menuProjectionChanges{snapshot: snapshot}
	for i := range bindings {
		bindings[i].Changes = changes
	}
	project := layoutgate.MenuProjection(runtime, nil)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "menu-provider", Actor: resolve, Verify: verify, Authorize: func(_ context.Context, a identity.VerifiedActor, u identity.ResourceURI, _ string) error {
		if a.Subject != "owner" || u.Namespace != "team" {
			return identity.ErrResourceDenied
		}
		return nil
	}, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"navigation": resources.ValidateMenu}, MenuVisibilityProjection: func(ctx context.Context, _ identity.VerifiedActor, _ identity.ResourceURI, raw json.RawMessage) (json.RawMessage, error) {
		return project(ctx, raw)
	}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "navigation", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "menu-provider", ResourceCacheScopes: map[string]string{"team/navigation": mcpcfg.ResourceCacheUser}}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	gw := resources.NewGateway(mgr, resolve, verify, "host")
	defer gw.Close()
	reader := NewGatewayLayoutMenuReader(gw, []GatewayMenuSource{{Resource: identity.ResourceRef{URI: def.URI}, ProviderIdentity: "menu-provider"}})
	heldProbe, err := reader(ctx)
	require.NoError(t, err)
	require.NoError(t, heldProbe.Revalidate(ctx))
	handler.SetLayoutMenuReader(reader)
	for i := 0; i < 3; i++ {
		got, err := handler.Layout(ctx, &LayoutRequest{})
		require.NoError(t, err)
		require.Len(t, got.Layout.Applications, 2)
	}
	held, err := reader(ctx)
	require.NoError(t, err)
	facts.features = nil
	require.NoError(t, held.Revalidate(ctx), "unnotified owning projection stays in the private metadata cache")
	held, err = reader(ctx)
	require.NoError(t, err)
	changes.projections.Publish(ctx)
	require.Error(t, held.Revalidate(ctx), "owning visibility notification cannot release the prior projection")
	require.NoError(t, held.Close())
	got, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Len(t, got.Layout.Applications, 1)
	require.Equal(t, "public", got.Layout.Applications[0].ID)
	require.NoError(t, handler.AuthorizeRemoteWindow(ctx, "operations-ui", "same"), "public ancestor remains independent from protected identical action")
	require.Equal(t, 1, loads)
	changed := bytes.Replace(raw, []byte("id: original"), []byte("id: reloaded"), 1)
	require.NoError(t, os.WriteFile(file, changed, 0600))
	frozen, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, "original", frozen.Layout.ID)
	require.NoError(t, handler.AuthorizeRemoteWindow(ctx, "operations-ui", "same"), "visible public ancestor remains allowed despite an unobserved file edit")
	require.Equal(t, 1, loads)
	require.NoError(t, snapshot.Reload(ctx))
	reloaded, err := handler.Layout(ctx, &LayoutRequest{})
	require.NoError(t, err)
	require.Equal(t, "reloaded", reloaded.Layout.ID)
	require.NoError(t, handler.AuthorizeRemoteWindow(ctx, "operations-ui", "same"))
	require.Equal(t, 2, loads)
}

type mutableLayoutFacts struct{ features []string }

func (r *mutableLayoutFacts) Resolve(context.Context, *permittedview.Request) (*permittedview.Snapshot, error) {
	return &permittedview.Snapshot{AuthorizationVersion: "original", ExpiresAt: time.Now().Add(time.Minute), Principal: map[string]any{"features": r.features}}, nil
}

// Menu projection facts and immutable authored source changes use the same
// standard owning MCP resource notification channel.
type menuProjectionChanges struct {
	snapshot    *resources.NativeAssetSnapshot
	projections producer.ResourceChanges
}

func (s *menuProjectionChanges) SubscribeResources(sink transport.Notifier) func() {
	stopSource := s.snapshot.SubscribeResources(sink)
	stopProjection := s.projections.SubscribeResources(sink)
	return func() { stopSource(); stopProjection() }
}
