package resource

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	bridge "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/service/ui/permittedview"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestCachedWindowProjectionKeepsInputAndSeparateSelectionsIsolated(t *testing.T) {
	ctx := context.Background()
	provider, source, actor, _ := localFixture(t)
	var window types.Window
	require.NoError(t, json.Unmarshal([]byte(`{"dataSource":{"owner":{"parameters":[{"name":"Id","from":"windowForm"}],"service":{"endpoint":"owner","uri":"/data"}}},"parameters":[{"name":"opaque","value":"unchanged"}],"view":{"content":{"id":"root","containers":[{"id":"edit","visibleWhen":{"source":"authorization","field":"principal.roles","contains":"editor"}},{"id":"plain"}]}}}`), &window))
	descriptor := json.RawMessage(`{"id":"owner","backend":{"kind":"mcp","connection":"owner","operation":"data/get"}}`)
	descriptorFingerprint, err := types.WindowDescriptorFingerprint(descriptor)
	require.NoError(t, err)
	window.ResourceDependencies = map[string]string{"owner": descriptorFingerprint}
	variant := types.WindowResourceVariant{Window: &window, DataSources: map[string]json.RawMessage{"owner": descriptor}}
	fingerprint, err := types.WindowVariantFingerprint(variant)
	require.NoError(t, err)
	source.raw, err = json.Marshal(types.WindowResourceEnvelope{SchemaVersion: 2, Format: types.WindowBundleFormat, Targets: []types.WindowTargetBinding{{Variant: fingerprint}}, Variants: map[string]types.WindowResourceVariant{fingerprint: variant}})
	require.NoError(t, err)
	reads := 0
	source.onRead = func() { reads++ }
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "owner", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: provider.ProviderIdentity()}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(provider) }))
	gateway := NewGateway(mgr, func(context.Context) (identity.VerifiedActor, error) { return *actor, nil }, func(context.Context, identity.VerifiedActor) error { return nil }, "projection")
	defer gateway.Close()
	proof, err := types.NewWindowTargetHMAC([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	catalog := &WindowCatalog{Gateway: gateway, TargetProof: proof}
	facts := profilePresentationFacts{snapshot: &permittedview.Snapshot{AuthorizationVersion: "roles", ExpiresAt: actor.ValidUntil, Principal: map[string]any{"roles": []string{"editor"}}}}
	runtime := permittedview.NewRuntime(facts)
	first, err := catalog.Get(ctx, &bridge.WindowDefinitionGetInput{WindowID: "window://team/sales"})
	require.NoError(t, err)
	before, err := json.Marshal(first.Definition)
	require.NoError(t, err)
	ownerDescriptors, err := json.Marshal(first.Definition.DataSource)
	require.NoError(t, err)
	projected, err := runtime.Project(ctx, first.Definition)
	require.NoError(t, err)
	after, err := json.Marshal(first.Definition)
	require.NoError(t, err)
	require.Equal(t, before, after, "projection itself never mutates the caller's selected definition")
	require.Equal(t, first.Definition.ResourceTarget, projected.Window.ResourceTarget)
	require.Equal(t, first.Definition.DataSource, projected.Window.DataSource)
	require.NoError(t, proof.Verify(ctx, *projected.Window.Resource, *projected.Window.ResourceTarget, fingerprint, projected.Window.ResourceTarget.SelectionToken))
	projected.Window.View.Content.ID = "caller-mutated"
	projected.Window.DataSource["owner"] = types.DataSource{}
	projected.Window.Parameters[0]["value"] = "caller-mutated"
	second, err := catalog.Get(ctx, &bridge.WindowDefinitionGetInput{WindowID: "window://team/sales"})
	require.NoError(t, err)
	secondProjection, err := runtime.Project(ctx, second.Definition)
	require.NoError(t, err)
	require.Equal(t, "root", secondProjection.Window.View.Content.ID)
	secondDescriptors, err := json.Marshal(secondProjection.Window.DataSource)
	require.NoError(t, err)
	require.Equal(t, ownerDescriptors, secondDescriptors, "projection keeps selected transport descriptors and owner arguments exact")
	require.Equal(t, "unchanged", secondProjection.Window.Parameters[0]["value"])
	require.Equal(t, 2, reads, "a second working selection reads its owning definition freshly")
	require.NoError(t, proof.Verify(ctx, *secondProjection.Window.Resource, *secondProjection.Window.ResourceTarget, fingerprint, secondProjection.Window.ResourceTarget.SelectionToken))
}
