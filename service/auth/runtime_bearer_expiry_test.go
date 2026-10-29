package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	tokenprovider "github.com/viant/agently-core/internal/auth/token"
)

func TestVerifiedBearerSurvivesToolTokenPreparation(t *testing.T) {
	private, public := generateRSAKeyPair(t, t.TempDir())
	cfg := &Config{Enabled: true, OAuth: &OAuth{Mode: "bff"}, JWT: &JWT{Enabled: true, RSA: []string{public}}}
	jwtService := NewJWTService(cfg.JWT)
	require.NoError(t, jwtService.Init(context.Background()))
	runtime := &Runtime{cfg: cfg, jwtService: jwtService, jwtVerifier: jwtService.verifier}
	signed := signTestJWT(t, private, map[string]interface{}{"sub": "reader"}, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/v1/workspace/layout", nil)
	req.Header.Set("Authorization", "Bearer "+signed)
	user, _ := runtime.authenticate(req)
	require.NotNil(t, user)
	ctx := withRuntimeAuthUser(req.Context(), user)
	owner, err := LocalStateOwner(ctx)
	require.NoError(t, err)
	require.NotNil(t, owner)
	prepared, err := tokenprovider.NewManager().EnsureTokens(ctx, tokenprovider.Key{Subject: "reader", Provider: "jwt"})
	require.NoError(t, err)
	require.True(t, signed == iauth.IDToken(prepared), "a fresh verified bearer must not be cleared or require a stored OAuth token")
	require.Equal(t, user.ValidUntil, user.Tokens.Expiry, "token preparation must use the verified signed expiry")
	next, err := LocalStateOwner(prepared)
	require.NoError(t, err)
	require.Equal(t, owner, next)
	req.Header.Set("Authorization", "Bearer "+signTestJWT(t, private, map[string]interface{}{"sub": "reader"}, -time.Minute))
	expired, _ := runtime.authenticate(req)
	require.Nil(t, expired, "expired signed credentials remain rejected")
}
