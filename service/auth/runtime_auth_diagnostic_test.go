package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRuntimeBearerDiagnosticSeparatesScopeRejection(t *testing.T) {
	privatePath, publicPath := generateRSAKeyPair(t, t.TempDir())
	cfg := &Config{Enabled: true, OAuth: &OAuth{Mode: "bff", Client: &OAuthClient{Scopes: []string{"openid", "fixture-required-scope"}}}, JWT: &JWT{Enabled: true, RSA: []string{publicPath}}}
	service := NewJWTService(cfg.JWT)
	require.NoError(t, service.Init(context.Background()))
	runtime := &Runtime{cfg: cfg, jwtService: service, jwtVerifier: service.verifier}
	for _, test := range []struct {
		name, scope, reason string
		authenticated       bool
	}{
		{"verified but under scoped", "openid", "bearer_oauth_scope_rejected", false},
		{"verified with configured scopes", "openid fixture-required-scope", "bearer_verified_but_no_runtime_user", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := signTestJWT(t, privatePath, map[string]interface{}{"sub": "fixture-user", "scope": test.scope, "privateClaim": "diagnostic-private-canary"}, time.Hour)
			request := httptest.NewRequest(http.MethodGet, "/v1/workspace/metadata", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			user, missing := runtime.authenticate(request)
			require.False(t, missing)
			require.Equal(t, test.authenticated, user != nil, "diagnostics must preserve authentication decisions")
			if user != nil {
				require.Equal(t, "fixture-user", user.EffectiveUserID, "canonical lookup absence retains bearer identity")
			}
			response := httptest.NewRecorder()
			writeRuntimeAuthDebugHeaders(response, request, runtime)
			require.Equal(t, test.reason, response.Header().Get("X-Auth-Reason"))
			headers := strings.Join(response.Header().Values("X-Auth-Reason"), " ")
			require.NotContains(t, headers, token)
			require.NotContains(t, headers, test.scope)
			require.NotContains(t, headers, "fixture-required-scope")
			require.NotContains(t, headers, "diagnostic-private-canary")
			require.Empty(t, response.Header().Get("X-Auth-Verify-Error"), "a scope failure is not a signature verification error")
		})
	}
}
