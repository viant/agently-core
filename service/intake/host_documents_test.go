package intake

import (
	"context"
	"github.com/stretchr/testify/require"
	agent "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/binding"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	"github.com/viant/agently-core/service/resource"
	bundlerepo "github.com/viant/agently-core/workspace/repository/toolbundle"
	"github.com/viant/mcp"
	client "github.com/viant/mcp/client"
	"os"
	"path/filepath"
	"testing"
)

func TestIntakeHostDocumentsUseNativeReaderAndPreserveImports(t *testing.T) {
	root := t.TempDir()
	for name, text := range map[string]string{"intake/prompt.tmpl": "Original prompt\n$import(parts/detail.md)\n", "intake/parts/detail.md": "Original imported rule."} {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(text), 0600))
	}
	snapshot, files, err := resource.CaptureHostDocuments(context.Background(), root)
	require.NoError(t, err)
	actor, verify := resource.PublicReadActors()
	provider, err := resource.NewLocalProvider(resource.LocalConfig{ProviderIdentity: "owner", Actor: actor, Verify: verify, Authorize: func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error {
		return identity.ErrResourceDenied
	}, NativeResources: snapshot.NativeResourceDocuments()})
	require.NoError(t, err)
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(context.Background(), "internal", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "owner"}, func(context.Context) (client.Interface, error) { return resource.NewLocalMCPClient(provider) }))
	t.Cleanup(func() { mgr.CloseConversation("") })
	gateway := resource.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(gateway.Close)
	reader := &resource.HostDocuments{Root: root, Files: files, Gateway: gateway, Provider: "owner", Connection: "internal"}
	service := &Service{}
	service.SetHostDocuments(reader, bundlerepo.NewWithReader(reader))
	prompt, err := service.resolveIntakePromptText(context.Background(), agent.IntakePrompt{Prompt: binding.Prompt{URI: "intake/prompt.tmpl"}})
	require.NoError(t, err)
	require.Contains(t, prompt, "Original imported rule.")
	inline, err := service.resolveIntakePromptText(context.Background(), agent.IntakePrompt{Prompt: binding.Prompt{Text: "$import(parts/detail.md)"}})
	require.NoError(t, err)
	require.Equal(t, "Original imported rule.", inline)
	require.NoError(t, os.WriteFile(filepath.Join(root, "intake/parts/detail.md"), []byte("changed rule"), 0600))
	_, err = service.resolveIntakePromptText(context.Background(), agent.IntakePrompt{Prompt: binding.Prompt{URI: "intake/prompt.tmpl"}})
	require.Error(t, err, "configured source must not fall back to fresh filesystem bytes")
}
