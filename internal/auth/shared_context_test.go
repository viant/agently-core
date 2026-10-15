package auth

import (
	"context"
	"golang.org/x/oauth2"
	"testing"

	"github.com/stretchr/testify/require"
	shared "github.com/viant/mcp/client/auth/link"
	scyauth "github.com/viant/scy/auth"
)

func TestCoreAndSharedMCPTransportContextsInteroperate(t *testing.T) {
	host := WithTokens(WithCanonicalUserID(WithProvider(WithUserInfo(context.Background(), &UserInfo{Subject: "subject", Email: "email@example.test"}), "idp"), "canonical-user"), &scyauth.Token{Token: oauth2.Token{AccessToken: "opaque-access"}, IDToken: "opaque-id"})
	require.Equal(t, "subject", shared.EffectiveUserID(host))
	require.Equal(t, "canonical-user", shared.CanonicalUserID(host))
	require.Equal(t, "idp", shared.Provider(host))
	require.Equal(t, "opaque-id", shared.MCPAuthToken(host, true))
	peer := shared.InjectTokens(shared.WithCanonicalUserID(shared.WithUserInfo(context.Background(), &shared.UserInfo{Subject: "other-subject"}), "other-user"), &scyauth.Token{Token: oauth2.Token{AccessToken: "other-access"}})
	require.Equal(t, "other-subject", EffectiveUserID(peer))
	require.Equal(t, "other-user", CanonicalUserID(peer))
	require.Equal(t, "other-access", MCPAuthToken(peer, false))
	masked := WithoutTokens(WithBearer(WithIDToken(host, "legacy-id"), "legacy-access"))
	require.Empty(t, shared.MCPAuthToken(masked, true))
	require.Empty(t, MCPAuthToken(masked, false))
	require.Equal(t, "subject", shared.EffectiveUserID(masked))
	require.Equal(t, "canonical-user", shared.CanonicalUserID(masked))
	require.Equal(t, "opaque-id", shared.MCPAuthToken(host, true), "masking a child context must not mutate its parent")
}
