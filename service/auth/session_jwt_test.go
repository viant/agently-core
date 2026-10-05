package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestJWTOnlySessionImportVerifiesBeforeIssuingCookie(t *testing.T) {
	private, public := generateRSAKeyPair(t, t.TempDir())
	otherPrivate, _ := generateRSAKeyPair(t, t.TempDir())
	claims := map[string]interface{}{"sub": "verified-user", "email": "verified@example.test", "scope": "read"}
	valid := signTestJWT(t, private, claims, time.Hour)
	expired := signTestJWT(t, private, claims, -time.Hour)
	foreign := signTestJWT(t, otherPrivate, claims, time.Hour)
	otherIdentity := signTestJWT(t, private, map[string]interface{}{"sub": "different-user"}, time.Hour)
	untrustedRefresh := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"scope":"admin","sub":"spoofed-user"}`)) + ".unverified"
	noIdentity := signTestJWT(t, private, map[string]interface{}{"email": "only-email@example.test"}, time.Hour)
	for _, implementation := range []string{"extension", "legacy"} {
		for _, tc := range []struct {
			name   string
			body   map[string]string
			bearer string
			valid  bool
		}{
			{name: "missing", body: map[string]string{"username": "spoofed-user"}},
			{name: "invalid body", body: map[string]string{"accessToken": "invalid-test-jwt"}},
			{name: "invalid header", bearer: "invalid-test-jwt"},
			{name: "expired", body: map[string]string{"idToken": expired}},
			{name: "foreign signing", body: map[string]string{"idToken": foreign}},
			{name: "missing signed identity", body: map[string]string{"idToken": noIdentity}},
			{name: "conflicting signed identities", body: map[string]string{"idToken": valid, "accessToken": otherIdentity}},
			{name: "valid body ignores caller identity", body: map[string]string{"username": "spoofed-user", "idToken": valid, "refreshToken": untrustedRefresh}, valid: true},
			{name: "valid bearer", bearer: valid, valid: true},
		} {
			t.Run(implementation+"/"+tc.name, func(t *testing.T) {
				cfg := &Config{Enabled: true, CookieName: "test_session", JWT: &JWT{Enabled: true, RSA: []string{public}}}
				manager := NewManager(time.Hour, nil)
				var handler http.Handler
				if implementation == "extension" {
					handler = newAuthExtension(cfg, manager, "", nil, nil).handleCreateSession()
				} else {
					handler = NewHandler(cfg, manager).handleCreateSession()
				}
				body, _ := json.Marshal(tc.body)
				request := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", bytes.NewReader(body))
				if tc.bearer != "" {
					request.Header.Set("Authorization", "Bearer "+tc.bearer)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, request)
				if !tc.valid {
					require.Equal(t, http.StatusUnauthorized, rec.Code)
					require.Empty(t, rec.Result().Cookies())
					require.Empty(t, manager.ActiveSessions())
					return
				}
				require.Equal(t, http.StatusOK, rec.Code)
				cookies := rec.Result().Cookies()
				require.Len(t, cookies, 1)
				session := manager.Get(context.Background(), cookies[0].Value)
				require.NotNil(t, session)
				require.Equal(t, "verified-user", session.Subject)
				require.Equal(t, "verified-user", session.Username)
				require.Equal(t, "verified@example.test", session.Email)
				require.Equal(t, []string{"read"}, session.Scopes)
			})
		}
	}
}
