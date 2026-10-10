package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/service/auth/providerregistry"
	"github.com/viant/agently-core/workspace/repository/oauthprovider"
	authcfg "github.com/viant/mcp/client/auth/config"
)

func TestMCPConnectorCatalogUsesSamePerUserCredentials(t *testing.T) {
	f := newMCPLinkFixture(t)
	ctx := context.Background()
	link, err := f.service.resolveServer(ctx, testMCPServer)
	require.NoError(t, err)
	// Settings must include optional providers without making them eager.
	rows, err := f.service.connectors(ctx, f.canonical, f.session.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.False(t, rows[0].Connected)
	require.True(t, rows[0].Available)
	require.Empty(t, f.service.eagerStatuses(ctx, f.canonical, f.session.ID))
	grant, err := f.service.validateGrant(ctx, link, (&oauth2.Token{AccessToken: f.accessJWT, Expiry: time.Now().Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"}), "")
	require.NoError(t, err)
	token := (&oauth2.Token{AccessToken: f.accessJWT, Expiry: time.Now().Add(time.Hour)}).WithExtra(map[string]interface{}{"scope": "plan:read"})
	require.NoError(t, f.service.persistVerifiedGrant(ctx, link, token, grant, f.canonical, ""))
	rows, err = f.service.connectors(ctx, f.canonical, f.session.ID)
	require.NoError(t, err)
	require.True(t, rows[0].Connected)
	credential, err := f.delegated.resolver.Resolve(delegatedCtx(f.canonical), *link.requirement)
	require.NoError(t, err)
	require.Equal(t, f.accessJWT, credential.Token)
	other, err := f.service.connectors(ctx, "another-user", f.session.ID)
	require.NoError(t, err)
	require.False(t, other[0].Connected)
	raw, err := json.Marshal(rows)
	require.NoError(t, err)
	require.NotContains(t, string(raw), f.accessJWT)
	require.NotContains(t, string(raw), "configURL")
	require.NotContains(t, string(raw), "csrfToken")

	f.service.disconnect(ctx, testMCPServer, f.canonical, f.canonical)
	rows, err = f.service.connectors(ctx, f.canonical, f.session.ID)
	require.NoError(t, err)
	require.False(t, rows[0].Connected)
	_, err = f.delegated.resolver.Resolve(delegatedCtx(f.canonical), *link.requirement)
	require.True(t, authcfg.IsLinkRequired(err), "next tool credential lookup must require authorization: %v", err)
}

func TestMCPConnectorCatalogRequiresSessionAndListsUnboundProviders(t *testing.T) {
	f := newMCPLinkFixture(t)
	rec := httptest.NewRecorder()
	f.mux().ServeHTTP(rec, f.request(http.MethodGet, "/v1/api/auth/mcp/connectors", false, ""))
	require.Equal(t, 401, rec.Code)
	doc := dev6ProviderDoc(false)
	f.delegated.registry = providerregistry.NewWithLoader(&fakeProviderLoader{docs: map[string]*oauthprovider.Document{doc.ID: doc}})
	rec = httptest.NewRecorder()
	f.mux().ServeHTTP(rec, f.request(http.MethodGet, "/v1/api/auth/mcp/connectors", true, ""))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), "No MCP server configured")
	require.NotContains(t, rec.Body.String(), "scy://")
}
