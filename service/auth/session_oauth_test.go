package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type sessionOAuthAuthority struct {
	server    *httptest.Server
	key       *rsa.PrivateKey
	configURL string
}

func newSessionOAuthAuthority(t *testing.T) *sessionOAuthAuthority {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	a := &sessionOAuthAuthority{key: key}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration", "/discovery":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": a.server.URL, "jwks_uri": a.server.URL + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fixture", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(a.server.Close)
	a.configURL = filepath.Join(t.TempDir(), "oauth.json")
	data, _ := json.Marshal(map[string]any{"clientID": "client", "authURL": a.server.URL + "/authorize", "tokenURL": a.server.URL + "/token"})
	require.NoError(t, os.WriteFile(a.configURL, data, 0600))
	return a
}
func (a *sessionOAuthAuthority) config(fallback bool) *Config {
	c := &OAuthClient{Issuer: a.server.URL, JWKSURL: a.server.URL + "/keys", ClientID: "client", Audiences: []string{"resource"}, Scopes: []string{"read"}}
	if fallback {
		c = &OAuthClient{ConfigURL: a.configURL, Scopes: []string{"read"}}
	}
	return &Config{Enabled: true, CookieName: "test_session", OAuth: &OAuth{Name: "provider", Mode: "bff", Client: c}}
}
func (a *sessionOAuthAuthority) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "fixture"
	raw, err := token.SignedString(a.key)
	require.NoError(t, err)
	return raw
}
func (a *sessionOAuthAuthority) claims(audience string) jwt.MapClaims {
	return jwt.MapClaims{"iss": a.server.URL, "sub": "user-123", "preferred_username": "devuser", "email": "verified@example.test", "aud": audience, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "scope": "read"}
}
func (a *sessionOAuthAuthority) pair(t *testing.T, audience string) (string, string) {
	access := a.sign(t, a.claims(audience))
	claims := a.claims("client")
	hash := sha256.Sum256([]byte(access))
	claims["at_hash"] = base64.RawURLEncoding.EncodeToString(hash[:len(hash)/2])
	return access, a.sign(t, claims)
}
func TestOAuthSessionImportVerifiedAuthorityAndAuditedFallback(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	for _, fallback := range []bool{false, true} {
		for _, implementation := range []string{"extension", "legacy", "legacy-oob"} {
			t.Run(implementation+map[bool]string{false: "/explicit", true: "/config-discovery"}[fallback], func(t *testing.T) {
				audience := "resource"
				if fallback {
					audience = "client"
				}
				access, id := a.pair(t, audience)
				cfg := a.config(fallback)
				sessions := NewManager(time.Hour, nil)
				var handler http.Handler
				switch implementation {
				case "extension":
					handler = newAuthExtension(cfg, sessions, "", nil, nil).handleCreateSession()
				case "legacy":
					handler = NewHandler(cfg, sessions).handleCreateSession()
				default:
					handler = NewHandler(cfg, sessions).handleOOB()
				}
				body, _ := json.Marshal(map[string]string{"username": "spoofed-user", "accessToken": access, "idToken": id, "refreshToken": "opaque-refresh", "expiresAt": "2099-01-01T00:00:00Z"})
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", bytes.NewReader(body)))
				require.Equal(t, http.StatusOK, rec.Code, "verified fixture rejected")
				session := sessions.Get(context.Background(), rec.Result().Cookies()[0].Value)
				require.NotNil(t, session)
				require.Equal(t, "user-123", session.Subject)
				require.Equal(t, "devuser", session.Username)
				require.Equal(t, "verified@example.test", session.Email)
				require.Equal(t, []string{"read"}, session.Scopes)
				require.True(t, session.Tokens.Expiry.Before(time.Now().Add(2*time.Hour)))
			})
		}
	}
}
func TestOAuthSessionImportRejectsUntrustedMaterialBeforeCookie(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	access, id := a.pair(t, "resource")
	for _, name := range []string{"missing", "opaque", "wrong issuer", "wrong audience", "expired", "missing expiration", "future issue", "different subject", "bad access hash", "unsigned", "wrong signature", "extra bad bearer"} {
		t.Run(name, func(t *testing.T) {
			currentAccess, currentID, bearer := access, id, ""
			claims := a.claims("resource")
			switch name {
			case "missing":
				currentAccess, currentID = "", ""
			case "opaque":
				currentAccess = "opaque-unverified"
			case "wrong issuer":
				claims["iss"] = "https://foreign.invalid"
				currentAccess = a.sign(t, claims)
			case "wrong audience":
				claims["aud"] = "foreign"
				currentAccess = a.sign(t, claims)
			case "expired":
				claims["exp"] = time.Now().Add(-time.Hour).Unix()
				currentAccess = a.sign(t, claims)
			case "missing expiration":
				delete(claims, "exp")
				currentAccess = a.sign(t, claims)
			case "future issue":
				claims["iat"] = time.Now().Add(time.Hour).Unix()
				currentAccess = a.sign(t, claims)
			case "different subject":
				claims["sub"] = "other"
				currentAccess = a.sign(t, claims)
			case "bad access hash":
				v := a.claims("client")
				v["at_hash"] = "invalid"
				currentID = a.sign(t, v)
			case "unsigned":
				currentID = "eyJhbGciOiJub25lIn0.e30."
			case "wrong signature":
				other, err := rsa.GenerateKey(rand.Reader, 2048)
				require.NoError(t, err)
				token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
				token.Header["kid"] = "fixture"
				currentAccess, err = token.SignedString(other)
				require.NoError(t, err)
			case "extra bad bearer":
				bearer = "invalid-explicit-token"
			}
			for _, legacy := range []bool{false, true} {
				cfg := a.config(false)
				sessions := NewManager(time.Hour, nil)
				var handler http.Handler = newAuthExtension(cfg, sessions, "", nil, nil).handleCreateSession()
				if legacy {
					handler = NewHandler(cfg, sessions).handleCreateSession()
				}
				body, _ := json.Marshal(map[string]string{"username": "spoofed-user", "accessToken": currentAccess, "idToken": currentID})
				req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", bytes.NewReader(body))
				if bearer != "" {
					req.Header.Set("Authorization", "Bearer "+bearer)
				}
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				require.Equal(t, http.StatusUnauthorized, rec.Code)
				require.Empty(t, rec.Result().Cookies())
				require.Empty(t, sessions.ActiveSessions())
			}
		})
	}
}

func TestMixedSessionImportLocalJWTUsesOnlyConfiguredAuthority(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	private, public := generateRSAKeyPair(t, t.TempDir())
	cfg := a.config(false)
	cfg.JWT = &JWT{Enabled: true, RSA: []string{public}}
	local := signTestJWT(t, private, map[string]interface{}{"sub": "local-user", "scope": "local:read"}, time.Hour)
	verified, err := verifyRawSessionImport(context.Background(), cfg, "", "", local)
	require.NoError(t, err)
	require.Equal(t, "local-user", verified.Subject)
	require.Equal(t, "jwt", verified.Provider)
	require.False(t, requiresOAuthTokensForSession(cfg, "provider", &Session{Provider: verified.Provider}))
	_, err = verifyRawSessionImport(context.Background(), cfg, "", "", "invalid")
	require.Error(t, err)
	access, id := a.pair(t, "resource")
	_, err = verifyRawSessionImport(context.Background(), cfg, id, access, local)
	require.Error(t, err, "different authority header cannot override supplied OAuth material")
}
