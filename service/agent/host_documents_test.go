package agent

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	"github.com/viant/agently-core/protocol/intent"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	bundle "github.com/viant/agently-core/protocol/tool/bundle"
	"github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	client "github.com/viant/mcp/client"
	"os"
	"path/filepath"
	"testing"
)

func TestProfilelessBundlesAndPlaybooksUseNativeDocumentDelivery(t *testing.T) {
	root := t.TempDir()
	for name, raw := range map[string]string{"tools/bundles/common.yaml": "id: common\nmatch: [{name: original-read}]\n", "tools/instructions/webdriver.md": "Original captured webdriver instructions."} {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
	}
	snapshot, files, err := resource.CaptureHostDocuments(context.Background(), root)
	require.NoError(t, err)
	actor, verify := resource.PublicReadActors()
	provider, err := resource.NewLocalProvider(resource.LocalConfig{ProviderIdentity: "configured", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error {
		return identity.ErrResourceDenied
	}, NativeResources: snapshot.NativeResourceDocuments()})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "configured"}, func(context.Context) (client.Interface, error) { return resource.NewLocalMCPClient(provider) }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	gateway := resource.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	reader := &resource.HostDocuments{Gateway: gateway, Connection: "internal", Provider: "configured", Root: root, Files: files}
	service := &Service{}
	service.SetHostDocuments(reader)
	documents := &binding.Documents{}
	require.NoError(t, service.appendToolPlaybooks(context.Background(), []*llm.ToolDefinition{{Name: "webdriver:open"}}, documents))
	require.NotEmpty(t, documents.Items)
	loaded, err := service.loadBundles(context.Background())
	require.NoError(t, err)
	require.Equal(t, "original-read", loaded["common"].Match[0].Name)
	// An admitted intent owns its bundles independently of global document state.
	own := &intent.Profile{ResourceURI: "intent://owner/profile", ToolBundles: []string{"common"}, ToolBundleDefinitions: []*bundle.Bundle{{ID: "common", Match: []llm.Tool{{Name: "owning-intent-read"}}}}}
	require.NoError(t, os.WriteFile(filepath.Join(root, "tools/bundles/common.yaml"), []byte("id: common\nmatch: [{name: changed-filesystem-read}]\n"), 0600))
	loaded, err = service.loadBundles(withSelectedPromptProfile(context.Background(), own))
	require.NoError(t, err)
	require.Equal(t, "owning-intent-read", loaded["common"].Match[0].Name)
	captured, err := service.loadBundles(context.Background())
	require.NoError(t, err)
	require.Equal(t, "original-read", captured["common"].Match[0].Name, "profile-less consumer must retain the captured host generation")
}
