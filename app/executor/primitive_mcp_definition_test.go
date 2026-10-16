package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	primitive "github.com/viant/agently-core/protocol/primitive"
	auth "github.com/viant/agently-core/service/auth"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/mcp-protocol/schema"
	"gopkg.in/yaml.v3"
)

func TestDefaultMCPDefinitionsAreCapturedMetadataWithoutConnections(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	var calls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "not a definition read", 500)
	}))
	defer target.Close()
	require.NoError(t, os.MkdirAll(filepath.Join(root, workspace.KindMCP), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, workspace.KindMCP, "remote.yaml"), []byte("name: remote\ntransport: {type: streamable, url: '"+target.URL+"'}\nauth: {mode: oauth, providerRef: authored-provider, clientRef: original-client}\n"), 0600))
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	out := &Runtime{MCPManager: mgr, AuthConfig: &auth.Config{Enabled: true}, UIBridge: forge.NewService(&forge.Config{})}
	require.NoError(t, NewBuilder().configurePrimitiveProviders(out))
	defer out.Close(context.Background())
	options, err := mgr.Options(context.Background(), configurationMCPName)
	require.NoError(t, err)
	require.Equal(t, mcpcfg.ResourceCachePublic, options.ResourceCacheScopes["workspace"], "operator-authored configuration can bootstrap without a fabricated user")
	ctx := auth.InjectUser(context.Background(), "author")
	rows, err := resources.List[*mcpcfg.MCPClient](ctx, out.PrimitiveProviders, "mcp", "workspace")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "mcp://workspace/remote", rows[0].Resource.URI)
	_, err = resources.List[*mcpcfg.MCPClient](context.Background(), out.PrimitiveProviders, "mcp", "workspace")
	require.Error(t, err, "auth-enabled managing source still requires signed-in ingress")
	client, err := mgr.Get(ctx, "", configurationMCPName)
	require.NoError(t, err)
	read, err := client.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: rows[0].Resource.URI})
	require.NoError(t, err)
	var definition mcpcfg.MCPClient
	require.NoError(t, yaml.Unmarshal([]byte(read.Contents[0].Text), &definition))
	require.Equal(t, "authored-provider", definition.Auth.ProviderRef)
	require.Equal(t, "original-client", definition.Auth.ClientRef)
	caps, err := out.mcpDefinitions.provider.NamespaceGet(ctx, primitive.NamespaceGetRequest{Namespace: "workspace"})
	require.NoError(t, err)
	found := false
	for _, kind := range caps.Kinds {
		if kind.Kind == "mcp" {
			found = true
			require.Equal(t, []string{"list", "get"}, kind.Operations)
		}
	}
	require.True(t, found)
	require.Zero(t, calls.Load())
	_, err = mgr.Options(ctx, "remote")
	require.Error(t, err, "metadata publishing does not install the described MCP connection")
}

func TestMCPRuntimeLoadsCapturedReferencesAfterSourceFileRemoval(t *testing.T) {
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	t.Setenv("AGENTLY_STATE_PATH", t.TempDir())
	require.NoError(t, os.MkdirAll(filepath.Join(root, workspace.KindMCP), 0700))
	file := filepath.Join(root, workspace.KindMCP, "remote.yaml")
	require.NoError(t, os.WriteFile(file, []byte("name: remote\ntransport: {type: streamable, url: 'https://example.invalid/mcp'}\ntoolsListVisibility: public\nauth: {mode: oauth, providerRef: existing-provider, clientRef: existing-client}\n"), 0600))
	mgr, err := manager.New(manager.NewRepoProvider())
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	out := &Runtime{MCPManager: mgr}
	defer out.Close(context.Background())
	builder := NewBuilder()
	require.NoError(t, builder.configureMCPDefinitionSource(out))
	require.NoError(t, builder.bindMCPDefinitions(out))
	require.NoError(t, os.Remove(file))
	// A workspace repository fallback would now fail. Both old aliases and
	// canonical references must read the captured managing MCP definition.
	for _, name := range []string{"remote", "mcp://workspace/remote"} {
		options, err := mgr.Options(context.Background(), name)
		require.NoError(t, err)
		require.Equal(t, "https://example.invalid/mcp", options.Transport.URL)
		require.Equal(t, "existing-client", options.Auth.ClientRef)
		require.Equal(t, mcpcfg.ToolsListVisibilityPublic, options.ToolsListVisibility)
		options.Auth.ClientRef = "caller mutation"
	}
	names, err := mgr.Names(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []string{configurationMCPName, "remote"}, names)
	_, err = mgr.Options(context.Background(), "missing")
	require.Error(t, err)
}

func TestMCPRuntimeSupportsInitiallyEmptyConfiguration(t *testing.T) {
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	root := t.TempDir()
	old := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(old) })
	mgr, err := manager.New(manager.NewRepoProvider())
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	out := &Runtime{MCPManager: mgr}
	defer out.Close(context.Background())
	builder := NewBuilder()
	require.NoError(t, builder.configureMCPDefinitionSource(out))
	require.NoError(t, builder.bindMCPDefinitions(out))
	names, err := mgr.Names(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{configurationMCPName}, names)
	_, err = mgr.Options(context.Background(), "unknown-server")
	require.Error(t, err, "empty MCP configuration must preserve unknown-server errors")
	require.NoDirExists(t, filepath.Join(root, workspace.KindMCP), "source registration must not require an MCP directory")
}
