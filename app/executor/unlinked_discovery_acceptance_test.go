package executor_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	mcpauth "github.com/viant/agently-core/internal/auth/mcpauth"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	templatesvc "github.com/viant/agently-core/protocol/tool/service/template"
	auth "github.com/viant/agently-core/service/auth"
	forge "github.com/viant/agently-core/service/primitiveprovider"
	resources "github.com/viant/agently-core/service/resource"
	workspaceui "github.com/viant/agently-core/service/workspace"
	"github.com/viant/agently-core/workspace"
	tplrepo "github.com/viant/agently-core/workspace/repository/template"
	authcfg "github.com/viant/mcp/client/auth/config"
)

type unlinkedDiscoveryCredentials struct{}

func (unlinkedDiscoveryCredentials) Resolve(_ context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return nil, authcfg.NewLinkRequired(&requirement, errors.New("no stored connector credential"))
}
func (r unlinkedDiscoveryCredentials) Refresh(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return r.Resolve(ctx, requirement)
}
func (unlinkedDiscoveryCredentials) Invalidate(context.Context, authcfg.Requirement) error {
	return nil
}

// Exercise the default Core credential-error mapper and real shared manager
// preflight, not a client factory returning the upstream error directly.
func TestBuilderUnlinkedConnectorsPreserveTemplateAndLayoutDiscovery(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	t.Setenv("AGENTLY_WORKSPACE_NO_DEFAULTS", "1")
	t.Setenv("AGENTLY_DB_DRIVER", "sqlite")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	t.Setenv("AGENTLY_DB_PATH", filepath.Join(t.TempDir(), "runtime.db"))
	t.Setenv("AGENTLY_STATE_PATH", t.TempDir())
	var requests atomic.Int32
	endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unlinked discovery must not contact connector", 500)
	}))
	t.Cleanup(endpoint.Close)
	write := func(name, raw string) {
		file := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
		require.NoError(t, os.WriteFile(file, []byte(raw), 0600))
	}
	write("ui/layout.yaml", "version: 1\nid: steward\napplications:\n  - id: workspace\n    title: Workspace\n    menus: [{id: orders, title: Orders, action: {type: window, windowKey: order}}]\n")
	write("extension/forge/windows/order.yaml", "id: order\nwindowKey: order\ntitle: Orders\nview: {content: {id: order, items: []}}\n")
	write("templates/analytics_dashboard.yaml", "id: analytics_dashboard\nname: analytics_dashboard\nformat: json\ninstructions: Preserve the bid-potential result.\n")
	var providers []*authcfg.OAuthProvider
	for _, name := range []string{"asana", "jira"} {
		write("mcp/"+name+".yaml", fmt.Sprintf("name: %s\ntransport: {type: streamable, url: '%s/mcp'}\nauth: {mode: oauth, providerRef: %s, clientRef: web, resource: '%s/mcp'}\n", name, endpoint.URL, name, endpoint.URL))
		providers = append(providers, &authcfg.OAuthProvider{ID: name, Issuer: "https://identity.example/", DefaultClient: "web", Clients: map[string]*authcfg.OAuthClient{"web": {ConfigURL: "mem://client.json", RedirectURI: "https://app.example/callback", Confidential: true, UsePKCE: true}}})
	}
	registry, err := authcfg.NewStaticRegistry(providers...)
	require.NoError(t, err)
	mgr, err := manager.New(manager.NewRepoProvider(), manager.WithCredentialResolver(unlinkedDiscoveryCredentials{}), manager.WithProviderRegistry(registry))
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithMCPManager(mgr).WithUIBridge(forge.NewService(&forge.Config{})).WithSkipRegistryInitialize(true).Build(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rt.Close(context.Background())) })
	ctx := auth.InjectUser(context.Background(), "signed-in-user")
	t.Run("template get", func(t *testing.T) {
		service := templatesvc.New(tplrepo.NewWithReader(resources.NewTemplateReader(rt.PrimitiveProviders)), nil)
		get, err := service.Method("get")
		require.NoError(t, err)
		includeDocument := true
		var output templatesvc.GetOutput
		require.NoError(t, get(ctx, &templatesvc.GetInput{Name: "analytics_dashboard", IncludeDocument: &includeDocument}, &output))
		require.Equal(t, "analytics_dashboard", output.Name)
		require.True(t, output.IncludedDocument)
		require.Equal(t, "Preserve the bid-potential result.", output.Instructions)
	})
	t.Run("workspace layout", func(t *testing.T) {
		handler := workspaceui.NewMetadataHandler(nil, nil, "fixture")
		source := rt.NativeLayoutSource
		require.NotNil(t, source)
		require.NoError(t, handler.SetCapturedLayout(source.Root, source.Source, source.Raw, source.Check))
		handler.SetWindowResourceCatalog(rt.UIBridge)
		handler.SetLayoutMenuReader(workspaceui.NewGatewayLayoutMenuReader(rt.PrimitiveProviders, []workspaceui.GatewayMenuSource{{Resource: identity.ResourceRef{URI: "navigation://workspace/layout"}, ProviderIdentity: resources.InternalProviderIdentity}}))
		mux := http.NewServeMux()
		handler.Register(mux)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil).WithContext(ctx))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "Orders")
	})
	t.Run("explicit tools still require linking", func(t *testing.T) {
		for _, name := range []string{"asana", "jira"} {
			_, err := rt.Registry.Execute(ctx, name+"/list_tasks", map[string]interface{}{})
			var required *mcpauth.LinkRequiredError
			require.ErrorAs(t, err, &required)
			require.Equal(t, name, required.ServerName)
			require.Equal(t, name, required.ProviderRef)
		}
	})
	require.Zero(t, requests.Load(), "discovery and rejected explicit invocation must not initiate an OAuth/HTTP flow")
}
