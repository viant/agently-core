package executor

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	uihttp "github.com/viant/agently-core/adapter/http/ui"
	"github.com/viant/agently-core/protocol/mcp/manager"
	"github.com/viant/agently-core/service/auth"
	ui "github.com/viant/agently-core/service/primitiveprovider"
	"github.com/viant/agently-core/workspace"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestDefaultAuthenticatedUIBootstrapUsesSessionOwnerNotMetadataExpiry(t *testing.T) {
	root := t.TempDir()
	previous := workspace.Root()
	workspace.SetRoot(root)
	defer workspace.SetRoot(previous)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "extension/forge/windows"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "extension/forge/windows/original.yaml"), []byte("title: Original\nview: {content: {id: original}}\n"), 0600))
	cfg := &auth.Config{Enabled: true, CookieName: "owned_session", OAuth: &auth.OAuth{Mode: "mixed"}}
	mgr, err := manager.New(nil)
	require.NoError(t, err)
	defer mgr.CloseConversation("")
	rt := &Runtime{MCPManager: mgr, UIBridge: ui.NewService(&ui.Config{}), AuthConfig: cfg}
	builder := NewBuilder()
	require.NoError(t, builder.configurePrimitiveProviders(rt))
	builder.configureDefaultUIOwner(rt)
	owner, err := rt.UIBridge.UIIdentity(auth.InjectUser(context.Background(), "alice"))
	require.NoError(t, err)
	require.Equal(t, "alice", owner.Namespace)
	require.True(t, owner.SessionBound)
	require.True(t, owner.ValidUntil.IsZero())
	sessions := auth.NewManager(0, nil)
	for _, subject := range []string{"alice", "bob"} {
		sessions.Put(context.Background(), &auth.Session{ID: "owned-" + subject, Subject: subject, Tokens: &scyauth.Token{Token: oauth2.Token{AccessToken: "owned-access-" + subject}, IDToken: "owned-id-" + subject}})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ui", rt.UIBridge.Hub().ServeHTTPRPC)
	mux.Handle("/window/", uihttp.NewEmbeddedHandlerWithAuthorization(root, nil, nil, nil, uihttp.WithWindowResourceProvider(rt.UIBridge)))
	server := httptest.NewServer(auth.Protect(cfg, sessions)(mux))
	defer server.Close()
	client := func(subject string) *uiOwnedClient {
		jar, _ := cookiejar.New(nil)
		base, _ := url.Parse(server.URL)
		jar.SetCookies(base, []*http.Cookie{{Name: cfg.CookieName, Value: "owned-" + subject}})
		return &uiOwnedClient{client: &http.Client{Jar: jar}, base: server.URL}
	}
	alice, bob := client("alice"), client("bob")
	_, err = alice.rpc(t, "ui.hello", map[string]any{"clientId": "alice-client"})
	require.NoError(t, err)
	_, err = bob.rpc(t, "ui.hello", map[string]any{"clientId": "bob-client"})
	require.NoError(t, err)
	get := func(c *uiOwnedClient, session string) int {
		request, err := http.NewRequest("GET", server.URL+"/window/original?applyPermission=true&clientId=alice-client&windowId=owned-instance", nil)
		require.NoError(t, err)
		request.Header.Set("Mcp-Session-Id", session)
		response, err := c.client.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		return response.StatusCode
	}
	require.Equal(t, http.StatusOK, get(alice, alice.session), "timeless definition must bind authenticated UI instance")
	require.Equal(t, http.StatusForbidden, get(bob, alice.session), "foreign user cannot borrow UI session")
	require.Equal(t, http.StatusForbidden, get(alice, bob.session), "foreign session cannot bind current user's instance")
	borrowed := *bob
	borrowed.session = alice.session
	_, err = borrowed.rpc(t, "ui.snapshot", map[string]any{"clientId": "alice-client", "snapshot": map[string]any{}})
	require.Error(t, err)
}
