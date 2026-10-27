package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"github.com/viant/mcp-protocol/schema"

	"github.com/viant/agently-core/protocol/mcp/auth/integrate"
	"github.com/viant/jsonrpc"
	authcfg "github.com/viant/mcp/client/auth/config"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	cfg "github.com/viant/agently-core/protocol/mcp/config"
	"github.com/viant/agently-core/protocol/mcp/manager"
	identity "github.com/viant/agently-core/protocol/resource"
	resources "github.com/viant/agently-core/service/resource"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
)

func TestApplicationSourcesUseProviderAppsInWorkspaceOrder(t *testing.T) {
	ctx := context.Background()
	actor, verify := resources.PublicReadActors()
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	t.Cleanup(func() { mgr.CloseConversation("") })
	for _, name := range []string{"first", "second"} {
		registerApplicationFixture(t, mgr, name)
	}

	gateway := resources.NewGateway(mgr, actor, verify, "host")
	t.Cleanup(func() { gateway.Close() })
	page, err := resources.ListPageFrom[json.RawMessage](ctx, gateway, []resources.Connection{{Name: "first"}, {Name: "second"}}, "navigation", resources.CatalogQuery{Limit: 100}, false)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	reader, err := ApplicationMenuReader(gateway, []byte(`applicationSources:
 - {connection: second, resource: "navigation://second/main", applicationId: secondary}
 - {connection: first, resource: "navigation://first/absent", applicationId: hidden}
 - {connection: first, resource: "navigation://first/main", applicationId: primary}
applications: []
`))
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		read, err := reader(ctx)
		require.NoError(t, err)
		var apps []LayoutApplication
		require.NoError(t, json.Unmarshal(read.Applications, &apps))
		require.Len(t, apps, 2)
		require.Equal(t, "secondary", apps[0].ID)
		require.Equal(t, "Provider secondary", apps[0].Title)
		require.Equal(t, "database", apps[0].Icon)
		require.Equal(t, "primary", apps[1].ID)
		require.NoError(t, read.Revalidate(ctx))
		require.NoError(t, read.Close())
	}
}

func TestApplicationSourcesRejectInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{
		`applicationSources: [{connection: server, resource: "window://remote/main", applicationId: app}]`,
		`applicationSources: [{connection: server, resource: "navigation://remote/main"}]`,
		`applicationSources: [{resource: "navigation://remote/main", applicationId: app}]`,
	} {
		_, err := ApplicationMenuReader(nil, []byte(raw))
		require.Error(t, err)
	}
}

func registerApplicationFixture(t *testing.T, mgr *manager.Manager, name string, decorate ...func(mcpclient.Interface) mcpclient.Interface) {
	t.Helper()
	ctx := context.Background()
	actor, verify := resources.PublicReadActors()
	root := t.TempDir()
	raw := []byte(`applications:
  - id: primary
    title: Provider primary
    icon: chart
    menus: [{id: open, title: Open, action: {type: window, windowKey: orders}}]
  - id: secondary
    title: Provider secondary
    icon: database
    menus: [{id: open, title: Open, action: {type: window, windowKey: orders}}]
`)
	require.NoError(t, os.WriteFile(filepath.Join(root, "layout.yaml"), raw, 0600))
	def, err := resources.SnapshotLayoutMenuDefinition("layout.yaml", "navigation://"+name+"/main")
	require.NoError(t, err)
	snapshot, err := resources.NewNativeAssetSnapshot(ctx, root, []resources.NativeSnapshotDefinition{def})
	require.NoError(t, err)
	bindings, err := snapshot.Bindings(name, func(_ context.Context, a identity.VerifiedActor, _ identity.ResourceURI, _ string) (identity.ResourceRevisionPolicy, error) {
		return capturedMenuPolicy{a}, nil
	})
	require.NoError(t, err)
	provider, err := resources.NewLocalProvider(resources.LocalConfig{ProviderIdentity: name, Actor: actor, Verify: verify, Bindings: bindings,
		Authorize:  func(context.Context, identity.VerifiedActor, identity.ResourceURI, string) error { return nil },
		Validators: map[string]resources.LocalResourceValidator{"navigation": resources.ValidateMenu},
		MenuActionVisibility: func(context.Context, identity.VerifiedActor, identity.ResourceURI, json.RawMessage) (bool, error) {
			return true, nil
		},
	})
	require.NoError(t, err)
	require.NoError(t, mgr.RegisterLocal(ctx, name, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: name}, func(context.Context) (mcpclient.Interface, error) {
		client, err := resources.NewLocalMCPClient(provider)
		if err == nil && len(decorate) > 0 {
			client = decorate[0](client)
		}
		return client, err
	}))
}

// This interface matches the transport's status-preserving error without
// requiring consumers to couple optional discovery to a dependency version.
type applicationStatusError int

func (e applicationStatusError) Error() string       { return fmt.Sprintf("HTTP %d", e) }
func (e applicationStatusError) HTTPStatusCode() int { return int(e) }

func TestApplicationSourcesIsolateOnlyExpectedProviderAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		omitted bool
	}{
		{"unauthorized", jsonrpc.NewUnauthorizedError(401, nil), true},
		{"forbidden", applicationStatusError(403), true},
		{"protocol-auth-required", &jsonrpc.Error{Code: schema.Unauthorized, Message: "authentication required"}, true},
		{"link-required", &authcfg.OAuthLinkRequiredError{ServerName: "optional", ProviderRef: "fixture"}, true},
		{"headless-required", &integrate.HeadlessAuthRequiredError{Origin: "fixture.invalid"}, true},
		{"server-error", applicationStatusError(500), false},
		{"transport-error", errors.New("connection unavailable"), false},
		{"mixed", errors.Join(applicationStatusError(403), errors.New("transport unavailable")), false},
		{"host-identity", identity.ErrResourceDenied, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			actor, verify := resources.PublicReadActors()
			mgr, err := manager.New(nil)
			require.NoError(t, err)
			defer mgr.CloseConversation("")
			registerApplicationFixture(t, mgr, "internal")
			registerApplicationFixture(t, mgr, "another")
			require.NoError(t, mgr.RegisterLocal(ctx, "optional", &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "optional"}, func(context.Context) (mcpclient.Interface, error) { return nil, tc.failure }))
			gateway := resources.NewGateway(mgr, actor, verify, "host")
			defer gateway.Close()
			reader, err := ApplicationMenuReader(gateway, []byte(`applicationSources:
 - {connection: optional, resource: "navigation://optional/main", applicationId: primary}
 - {connection: internal, resource: "navigation://internal/main", applicationId: primary}
 - {connection: another, resource: "navigation://another/main", applicationId: secondary}
`))
			require.NoError(t, err)
			handler := NewMetadataHandler(nil, nil, "test")
			handler.SetLayoutDefault([]byte("version: 1\nid: fixture\napplications: []\n"))
			handler.SetLayoutMenuReader(reader)
			got, err := handler.Layout(ctx, &LayoutRequest{})
			if !tc.omitted {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, got.Layout.Applications, 2)
			require.Equal(t, "primary", got.Layout.Applications[0].ID)
			require.Equal(t, "secondary", got.Layout.Applications[1].ID)
			require.Equal(t, "internal", got.Layout.Applications[0].ProviderIdentity)
		})
	}
}

func TestApplicationSourcesHTTPDenialDoesNotPromptOrBreakLayout(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var prompts, requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("WWW-Authenticate", `Bearer authorization_uri="https://fixture.invalid/login"`)
				w.WriteHeader(status)
			}))
			defer server.Close()
			authRT, err := integrate.NewAuthRoundTripperWithElicitation(nil, http.DefaultTransport, 0, func(context.Context, string) error { prompts.Add(1); return errors.New("unexpected interactive auth") })
			require.NoError(t, err)
			options := &cfg.MCPClient{PrimitiveProviderIdentity: "optional", ClientOptions: &mcp.ClientOptions{Name: "optional", Version: "1", ProtocolVersion: schema.LatestProtocolVersion, Auth: &mcp.ClientAuth{}, Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}
			mgr, err := manager.New(applicationRemoteOptions{options}, manager.WithAuthRoundTripper(authRT))
			require.NoError(t, err)
			defer mgr.CloseConversation("")
			registerApplicationFixture(t, mgr, "internal")

			actor, verify := resources.PublicReadActors()
			gateway := resources.NewGateway(mgr, actor, verify, "host")
			defer gateway.Close()
			reader, err := ApplicationMenuReader(gateway, []byte(`applicationSources:
 - {connection: optional, resource: "navigation://optional/main", applicationId: primary}
 - {connection: internal, resource: "navigation://internal/main", applicationId: primary}
`))
			require.NoError(t, err)
			handler := NewMetadataHandler(nil, nil, "test")
			handler.SetLayoutDefault([]byte("version: 1\nid: fixture\napplications: []\n"))
			handler.SetLayoutMenuReader(reader)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			got, err := handler.Layout(ctx, &LayoutRequest{})
			require.Positive(t, requests.Load())
			require.Zero(t, prompts.Load())
			if status == 500 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, got.Layout.Applications, 1)
			require.Equal(t, "internal", got.Layout.Applications[0].ProviderIdentity)
		})
	}
}

type applicationRemoteOptions struct{ value *cfg.MCPClient }

func (p applicationRemoteOptions) Options(_ context.Context, name string) (*cfg.MCPClient, error) {
	if name != "optional" {
		return nil, fmt.Errorf("unconfigured fixture connection")
	}
	raw, err := json.Marshal(p.value)
	if err != nil {
		return nil, err
	}
	var copy cfg.MCPClient
	err = json.Unmarshal(raw, &copy)
	return &copy, err
}

func (applicationRemoteOptions) Names(context.Context) ([]string, error) {
	return []string{"optional"}, nil
}
