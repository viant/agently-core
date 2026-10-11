package resource

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestNativeMenuSharesSnapshotAndRetainsLiveVisibility(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := filepath.Join(root, "ui/layout.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
	original := []byte("version: 1\nid: original\ntopbar: {actions: [{id: header, title: Header, action: {type: window, windowKey: headerOnly}}]}\napplications:\n  - id: original\n    title: Original\n    menus:\n      - id: advertisers\n        title: Advertisers\n        action: {type: window, windowKey: advertiserList, parameters: {opaque: 9007199254740993}}\n      - id: campaigns\n        title: Campaigns\n        action: {type: window, windowKey: campaignList}\n")
	require.NoError(t, os.WriteFile(file, original, 0600))
	def, err := SnapshotLayoutMenuDefinition("ui/layout.yaml", "menu://team/layout")
	require.NoError(t, err)
	reads := 0
	native := def.Load
	def.Load = func(ctx context.Context, r *ExtensionReader, file string) (json.RawMessage, error) {
		reads++
		return native(ctx, r, file)
	}
	snapshot, err := NewNativeAssetSnapshot(ctx, root, []NativeSnapshotDefinition{def}, NativeSnapshotOptions{AssetPaths: []string{"ui/layout.yaml"}, ImmutableUntilRestart: true})
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	p, _, actor, revoked := menuFixtureProvider(t)
	bindings, err := snapshot.Bindings(p.identity, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return localFixturePolicy{actor: a}, nil
	})
	require.NoError(t, err)
	p.bindings = map[string]LocalResourceBinding{def.URI: bindings[0]}
	p.menuActionVisibility = func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, action json.RawMessage) (bool, error) {
		if strings.Contains(string(action), "advertiserList") {
			return a.Subject == "alice", nil
		}
		return true, nil
	}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	require.NoError(t, mgr.RegisterLocal(ctx, "local-menu", &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: p.identity}, func(context.Context) (mcpclient.Interface, error) { return NewLocalMCPClient(p) }))
	gateway := NewGateway(mgr, p.actor, p.verify, "host")
	defer gateway.Close()
	connection, err := gateway.ConnectionForProvider(ctx, p.identity)
	require.NoError(t, err)
	var fingerprint string
	for i := 0; i < 6; i++ {
		got, err := Get[json.RawMessage](ctx, gateway, connection, identity.ResourceRef{URI: def.URI}, nil)
		require.NoError(t, err)
		require.Empty(t, got.Resource.Definition)
		require.Empty(t, got.Resource.DefinitionBytes)
		require.Contains(t, string(got.Definition), "advertiserList")
		require.Contains(t, string(got.Definition), "9007199254740993")
		require.NotContains(t, string(got.Definition), "headerOnly")
		fingerprint = got.Resource.ContentFingerprint
	}
	require.Equal(t, 1, reads, "warm generic Get reads compiled snapshot, not YAML")
	actor.Subject = "bob"
	bob, err := Get[json.RawMessage](ctx, gateway, connection, identity.ResourceRef{URI: def.URI}, nil)
	require.NoError(t, err)
	require.NotContains(t, string(bob.Definition), "advertiserList")
	require.Contains(t, string(bob.Definition), "campaignList")
	require.Equal(t, fingerprint, bob.Resource.ContentFingerprint)
	actor.Subject = "alice"
	*revoked = true
	_, err = Get[json.RawMessage](ctx, gateway, connection, identity.ResourceRef{URI: def.URI}, nil)
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	*revoked = false
	require.NoError(t, os.WriteFile(file, append(original, []byte("\n# changed original\n")...), 0600))
	_, err = Get[json.RawMessage](ctx, gateway, connection, identity.ResourceRef{URI: def.URI}, nil)
	require.Error(t, err, "changed original cannot reuse startup definition")
	require.Equal(t, 1, reads, "changed immutable snapshot denies without YAML reload")
}
