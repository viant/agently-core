package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/sdk/api"
	resources "github.com/viant/agently-core/service/resource"
	workspacewindow "github.com/viant/agently-core/service/ui/window"
	"github.com/viant/agently-core/workspace"
	fsstore "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/forge/backend/types"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

type forbiddenLookupStore struct {
	workspace.Store
	calls          atomic.Int32
	unrelatedCalls atomic.Int32
}

func (s *forbiddenLookupStore) List(context.Context, string) ([]string, error) {
	s.calls.Add(1)
	return nil, fmt.Errorf("raw workspace lookup loading forbidden")
}

type lookupTestPolicy struct{ actor identity.VerifiedActor }

func (p lookupTestPolicy) SelectRevision(_ context.Context, _ identity.ResourceRef, candidates []identity.ResourceCandidate) (identity.ResourceDecision, error) {
	if len(candidates) != 1 {
		return identity.ResourceDecision{}, identity.ErrResourceDenied
	}
	return identity.ResourceDecision{Candidate: candidates[0], AuthorityBinding: "lookup-fixture", ValidUntil: p.actor.ValidUntil}, nil
}

func TestPrimitiveLookupRegistryUsesCapturedOriginalOverlayNotRawWorkspace(t *testing.T) {
	ctx := context.Background()
	backend, store := primitiveLookupFixture(t)
	first, err := backend.ListLookupRegistry(ctx, &api.ListLookupRegistryInput{Context: "window:order", ProviderIdentity: "lookup-owner"})
	require.NoError(t, err)
	require.Len(t, first.Entries, 1)
	entry := first.Entries[0]
	require.Equal(t, "order", entry.Name)
	require.Equal(t, "ad_order_lookup", entry.DataSource)
	require.Equal(t, "adOrderPicker", entry.DialogId)
	require.Equal(t, "${adOrderId}", entry.Token.Store)
	require.Equal(t, "${id}", entry.Token.ModelForm)
	require.Equal(t, "datasource://external/ad_order_lookup", entry.DataSourceResource.URI)
	require.Equal(t, "window://external/adOrderPicker", entry.PickerResource.URI)
	require.Equal(t, "lookup-owner", entry.ProviderIdentity)
	require.Equal(t, "adOrderId", entry.Outputs[0].Location)
	require.Equal(t, "order_id", entry.Outputs[0].Name)
	second, err := backend.ListLookupRegistry(ctx, &api.ListLookupRegistryInput{Context: "window:order", ProviderIdentity: "lookup-owner"})
	require.NoError(t, err)
	require.Equal(t, first, second)
	encoded, err := json.Marshal(entry)
	require.NoError(t, err)
	require.Contains(t, string(encoded), "pickerResource")
	service, err := backend.primitiveLookupService(ctx, nil, "lookup-owner")
	require.NoError(t, err)
	properties := map[string]interface{}{"order_id": map[string]interface{}{"type": "integer"}}
	applied := service.Apply("window", "order", properties)
	require.Len(t, applied, 1)
	projected := properties["order_id"].(map[string]interface{})["x-ui-lookup"].(map[string]interface{})
	require.Equal(t, "ad_order_lookup", projected["dataSource"])
	require.Equal(t, "datasource://external/ad_order_lookup", projected["dataSourceResource"].(*identity.ResourceRef).URI)
	require.Equal(t, "window://external/adOrderPicker", projected["pickerResource"].(*identity.ResourceRef).URI)
	require.Equal(t, "lookup-owner", projected["providerIdentity"])
	require.Zero(t, store.calls.Load(), "primitive lookup mode never enters legacy raw workspace loading")
	require.Zero(t, store.unrelatedCalls.Load(), "explicit managing owner bounds discovery before enumeration")
}

func primitiveLookupFixture(t testing.TB) (*backendClient, *forbiddenLookupStore) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	original, err := os.ReadFile("../service/resource/testdata/lookups/ad_order_lookup.yaml")
	require.NoError(t, err)
	path := filepath.Join(root, workspace.KindForgeLookup, "ad_order_lookup.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, original, 0600))
	definitions, err := resources.SnapshotExtensionDefinitions(ctx, root, []resources.ExtensionDirectory{resources.LookupDirectory("external", "extension/forge/lookups")})
	require.NoError(t, err)
	snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, definitions)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	bindings, err := snapshot.Bindings("lookup-owner", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return lookupTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "lookup-owner", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"lookup": resources.ValidateLookupDefinition}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "lookups", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "lookup-owner"}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	gateway := resources.NewGateway(mgr, actor, verify, "consumer")
	t.Cleanup(gateway.Close)
	store := &forbiddenLookupStore{Store: fsstore.New(root)}
	require.NoError(t, mgr.RegisterLocal(ctx, "unrelated", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "other-owner"}, func(context.Context) (mcpclient.Interface, error) {
		store.unrelatedCalls.Add(1)
		return nil, fmt.Errorf("unrelated MCP must not be contacted for an explicit lookup owner")
	}))
	backend := &backendClient{primitiveProviders: gateway, store: store}
	require.NoError(t, os.Remove(path), "captured managing source owns original bytes")

	return backend, store
}

func BenchmarkPrimitiveLookupRegistryWarm(b *testing.B) {
	backend, store := primitiveLookupFixture(b)
	ctx := context.Background()
	request := &api.ListLookupRegistryInput{Context: "window:order", ProviderIdentity: "lookup-owner"}
	if _, err := backend.ListLookupRegistry(ctx, request); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := backend.ListLookupRegistry(ctx, request); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if store.calls.Load() != 0 {
		b.Fatal("raw workspace scan")
	}
}

func TestPrimitiveLookupRegistryCapturedStewardChatOwner(t *testing.T) {
	root := os.Getenv("AGENTLY_STEWARD_FIXTURE_ROOT")
	if root == "" {
		t.Skip("set AGENTLY_STEWARD_FIXTURE_ROOT to the read-only captured Steward workspace")
	}
	ctx := context.Background()
	definitions, err := resources.SnapshotExtensionDefinitions(ctx, root, []resources.ExtensionDirectory{resources.LookupDirectory("workspace", workspace.KindForgeLookup)})
	require.NoError(t, err)
	chat := workspacewindow.ResourceBinding{WindowKey: "chat/new", URI: "window://system/chat/new"}
	definitions = append(definitions, resources.NativeSnapshotDefinition{URI: chat.URI, Title: "Chat", File: "embedded/window", FormatVersion: 2, Load: resources.NativeComposedWindowLoader(chat, func(context.Context, *types.WindowTarget) (*types.Window, error) {
		return &types.Window{WindowKey: "chat/new", View: types.View{Content: &types.Container{ID: "chat", Chat: &types.Chat{}}}}, nil
	}, nil)})
	snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, definitions)
	require.NoError(t, err)
	actor, verify := resources.PublicReadActors()
	bindings, err := snapshot.Bindings("captured", func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return lookupTestPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: "captured", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil }, Bindings: bindings, Validators: map[string]resources.LocalResourceValidator{"lookup": resources.ValidateLookupDefinition, "window": resources.ValidateWindowBundle}})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, "capture", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "captured"}, func(context.Context) (mcpclient.Interface, error) { return resources.NewLocalMCPClient(provider) }))
	gateway := resources.NewGateway(mgr, actor, verify, "consumer")
	defer gateway.Close()
	backend := &backendClient{primitiveProviders: gateway}
	registry, err := backend.ListLookupRegistry(ctx, &api.ListLookupRegistryInput{Context: "chat-composer:agent/steward", Owner: &identity.ResourceRef{URI: chat.URI}, ProviderIdentity: "captured"})
	require.NoError(t, err)
	found := false
	for _, entry := range registry.Entries {
		if entry.Name == "order" {
			found = true
			require.Equal(t, "adOrderPicker", entry.DialogId)
			require.Equal(t, "ad_order_lookup", entry.DataSource)
			require.Equal(t, "window://workspace/adOrderPicker", entry.PickerResource.URI)
		}
	}
	require.True(t, found, "captured original Order lookup must remain available for the owning chat composer")
}
