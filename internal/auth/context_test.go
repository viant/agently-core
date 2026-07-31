package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

func TestMCPAuthToken_ExpiredIDTokenFallsBackToFreshAccessToken(t *testing.T) {
	expiredIDToken := contextTestJWT(t, time.Now().Add(-time.Minute))
	ctx := WithTokens(context.Background(), &scyauth.Token{
		Token: oauth2.Token{
			AccessToken: "fresh-access-token",
			Expiry:      time.Now().Add(30 * time.Minute),
		},
		IDToken: expiredIDToken,
	})

	if got := MCPAuthToken(ctx, true); got != "fresh-access-token" {
		t.Fatalf("MCPAuthToken() = %q, want refreshed access token", got)
	}
}

func contextTestJWT(t *testing.T, expiry time.Time) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"exp": expiry.Unix()})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}
