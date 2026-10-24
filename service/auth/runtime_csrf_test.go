package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func cookieProtectedRuntime(t *testing.T) *Runtime {
	t.Helper()
	sessions := NewManager(time.Hour, nil)
	sessions.Put(context.Background(), &Session{ID: "verified-session", Subject: "alice", Provider: "local", ExpiresAt: time.Now().Add(time.Hour)})
	return &Runtime{cfg: &Config{Enabled: true, CookieName: "agently_session", Local: &Local{Enabled: true}}, sessions: sessions}
}

func TestRuntimeCookieUnsafeRoutesRequireSameOriginBeforeDispatch(t *testing.T) {
	for _, protect := range []struct {
		name string
		wrap func(*Runtime, http.Handler) http.Handler
	}{
		{"api", (*Runtime).protect},
		{"metadata", (*Runtime).protectAll},
	} {
		t.Run(protect.name, func(t *testing.T) {
			runtime := cookieProtectedRuntime(t)
			calls := 0
			handler := protect.wrap(runtime, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
			for _, tc := range []struct {
				name, origin, referer, fetchSite, forwarded string
				want                                        int
			}{
				{"same origin", "http://steward.test", "", "same-origin", "", http.StatusNoContent},
				{"same referer", "", "http://steward.test/ui", "", "", http.StatusNoContent},
				{"same fetch site", "", "", "same-origin", "", http.StatusNoContent},
				{"foreign origin", "http://intruder.test", "", "cross-site", "", http.StatusForbidden},
				{"same site sibling", "http://sibling.steward.test", "", "same-site", "", http.StatusForbidden},
				{"null origin", "null", "", "cross-site", "", http.StatusForbidden},
				{"missing browser proof", "", "", "", "", http.StatusForbidden},
				{"forged origin scheme", "http://steward.test", "", "same-origin", "https", http.StatusForbidden},
				{"reverse proxy https", "https://steward.test", "", "same-origin", "https", http.StatusNoContent},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "/v1/api/datasources/rows/fetch", nil)
					request.Host = "steward.test"
					request.AddCookie(&http.Cookie{Name: "agently_session", Value: "verified-session"})
					if tc.origin != "" {
						request.Header.Set("Origin", tc.origin)
					}
					if tc.referer != "" {
						request.Header.Set("Referer", tc.referer)
					}
					if tc.fetchSite != "" {
						request.Header.Set("Sec-Fetch-Site", tc.fetchSite)
					}
					if tc.forwarded != "" {
						request.Header.Set("X-Forwarded-Proto", tc.forwarded)
					}
					before := calls
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					require.Equal(t, tc.want, response.Code)
					if tc.want == http.StatusForbidden {
						require.Equal(t, before, calls, "cross-origin cookie request reached backend")
					} else {
						require.Equal(t, before+1, calls)
					}
				})
			}
		})
	}
}

func TestRuntimeCookieCheckUsesAuthenticatedSessionEvenWithInvalidBearerHeader(t *testing.T) {
	runtime := cookieProtectedRuntime(t)
	calls := 0
	handler := runtime.protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "/v1/tools/reporting:run_report/execute", nil)
	request.Host = "steward.test"
	request.Header.Set("Origin", "http://intruder.test")
	request.Header.Set("Authorization", "Bearer invalid")
	request.AddCookie(&http.Cookie{Name: "agently_session", Value: "verified-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Zero(t, calls)
}

func TestRuntimeAuthCallbackPostIsUnaffectedByCookieOriginCheck(t *testing.T) {
	runtime := cookieProtectedRuntime(t)
	handler := runtime.protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oauth/callback", nil)
	request.Host = "steward.test"
	request.Header.Set("Origin", "http://identity-provider.test")
	request.AddCookie(&http.Cookie{Name: "agently_session", Value: "verified-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestRuntimeVerifiedBearerPostDoesNotNeedBrowserOrigin(t *testing.T) {
	private, public := generateRSAKeyPair(t, t.TempDir())
	runtime := cookieProtectedRuntime(t)
	runtime.cfg.JWT = &JWT{Enabled: true, RSA: []string{public}}
	service := NewJWTService(runtime.cfg.JWT)
	require.NoError(t, service.Init(context.Background()))
	runtime.jwtService, runtime.jwtVerifier = service, service.verifier
	token := signTestJWT(t, private, map[string]interface{}{"sub": "bearer-client"}, time.Hour)
	calls := 0
	handler := runtime.protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodPost, "/v1/tools/reporting:run_report/execute", nil)
	request.Host = "steward.test"
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", "http://other.test")
	request.AddCookie(&http.Cookie{Name: "agently_session", Value: "verified-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, 1, calls)
}

func TestSessionCookieSecureOnHTTPSAndProxyHTTPS(t *testing.T) {
	for _, tc := range []struct {
		name, url, forwarded string
		secure               bool
	}{
		{"direct local http", "http://steward.test/login", "", false},
		{"direct https", "https://steward.test/login", "", true},
		{"proxy https", "http://steward.test/login", "https", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tc.url, nil)
			if tc.forwarded != "" {
				request.Header.Set("X-Forwarded-Proto", tc.forwarded)
			}
			response := httptest.NewRecorder()
			writeSessionCookie(response, request, &Config{CookieName: "agently_session"}, NewManager(time.Hour, nil), "verified-session")
			cookies := response.Result().Cookies()
			require.Len(t, cookies, 1)
			require.Equal(t, tc.secure, cookies[0].Secure)
			require.True(t, cookies[0].HttpOnly)
			require.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
		})
	}
}
