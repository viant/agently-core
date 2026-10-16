package auth

import (
	"context"
	"encoding/json"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnedWebSessionExpiredIDTokenUsesNormalRefresh(t *testing.T) {
	for _, variant := range []string{"same user", "response scope", "access scope", "other subject", "other issuer", "missing scope", "refresh rejected"} {
		t.Run(variant, func(t *testing.T) {
			authority := newSessionOAuthAuthority(t)
			cfg := authority.config(false)
			cfg.OAuth.Client.ConfigURL = authority.configURL
			rt, sess := runtimeRefreshHarness(authority.configURL, cfg.OAuth.Client, nil)
			rt.cfg = cfg
			rt.ext.cfg = cfg
			sess.Subject = "user-123"
			sess.Scopes = []string{"read"}
			sess.Provider = "provider"
			sess.Tokens.AccessToken = "still-valid-access"
			sess.Tokens.Expiry = time.Now().Add(time.Hour)
			old := authority.claims("client")
			old["exp"] = time.Now().Add(-time.Minute).Unix()
			sess.Tokens.IDToken = authority.sign(t, old)
			fresh := authority.claims("client")
			switch variant {
			case "response scope", "access scope":
				delete(fresh, "scope")
			case "other subject":
				fresh["sub"] = "other"
			case "other issuer":
				fresh["iss"] = "https://foreign.invalid"
			case "missing scope":
				fresh["scope"] = "other"
			}
			freshID := authority.sign(t, fresh)
			access := "new-access"
			if variant == "access scope" {
				access = authority.sign(t, authority.claims("resource"))
			}
			responseScope := "read"
			if variant == "missing scope" {
				responseScope = "other"
			}
			calls := 0
			previous := authority.server.Config.Handler
			authority.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					previous.ServeHTTP(w, r)
					return
				}
				calls++
				require.NoError(t, r.ParseForm())
				require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
				require.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
				require.Equal(t, "read", r.Form.Get("scope"))
				w.Header().Set("Content-Type", "application/json")
				if variant == "refresh rejected" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "id_token": freshID, "scope": responseScope})
			})
			rt.sessions.Put(context.Background(), sess)
			sessionExpiry := sess.ExpiresAt
			injected := ""
			handler := rt.protectAll(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				injected = iauth.IDToken(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
			req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: sess.ID})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			require.Equal(t, 1, calls, "valid access must not suppress expired-ID refresh")
			if variant == "same user" || variant == "response scope" || variant == "access scope" {
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, sessionExpiry, sess.ExpiresAt, "refresh must not extend session lifetime")
				require.Equal(t, freshID, injected)
				require.Equal(t, freshID, rt.sessions.Get(context.Background(), sess.ID).Tokens.IDToken)
				require.Equal(t, tokenAvailable, currentSessionOAuthToken(cfg.OAuth.Client, sess).state, "response scopes must remain usable on later requests")
			} else {
				require.Equal(t, http.StatusUnauthorized, rec.Code)
				require.Empty(t, injected)
			}
		})
	}
}

func TestOwnedSessionFreshIDTokenValidationRejectsExpiredAndWrongAudience(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	cfg := a.config(false)
	sess := &Session{Subject: "user-123"}
	for _, claims := range []jwt.MapClaims{a.claims("other-client"), func() jwt.MapClaims {
		c := a.claims("client")
		c["exp"] = time.Now().Add(-time.Minute).Unix()
		return c
	}()} {
		require.Error(t, validateOwnedSessionIDToken(context.Background(), cfg, sess, a.sign(t, claims)))
	}
}

func TestOwnedStoreIDRefreshAdvancesWithoutAccessExpiryAndRejectsForeignProvider(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	cfg := a.config(false)
	old := a.claims("client")
	old["exp"] = time.Now().Add(-time.Minute).Unix()
	fresh := a.sign(t, a.claims("client"))
	expiry := time.Now().Add(time.Hour)
	for _, provider := range []string{"provider", "mcp:foreign"} {
		t.Run(provider, func(t *testing.T) {
			store := &canceledCtxTokenStore{token: &OAuthToken{Username: "user-42", Provider: provider, AccessToken: "stored-access", IDToken: fresh, RefreshToken: "owned-refresh", ExpiresAt: expiry}}
			rt, sess := runtimeRefreshHarness(a.configURL, cfg.OAuth.Client, store)
			rt.cfg = cfg
			rt.ext.cfg = cfg
			sess.Provider = "provider"
			sess.Subject = "user-123"
			sess.Tokens.IDToken = a.sign(t, old)
			sess.Tokens.Expiry = expiry
			result := rt.tryLoadFreshTokenFromStore(context.Background(), sess)
			if provider == "provider" {
				require.Equal(t, tokenAvailable, result.state)
				require.Equal(t, fresh, result.token.IDToken)
			} else {
				require.Equal(t, tokenPreserveWithoutInjection, result.state)
				require.Nil(t, result.token)
			}
		})
	}
}

func TestOwnedSessionIdentityValidationPreservesOpaqueProviderContract(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	cfg := a.config(false)
	sess := &Session{Subject: "user-123"}
	require.NoError(t, validateOwnedSessionIDToken(context.Background(), cfg, sess, "opaque-id"))
	sess.Tokens = &scyauth.Token{IDToken: a.sign(t, a.claims("client"))}
	require.Error(t, validateOwnedSessionIDToken(context.Background(), cfg, sess, "opaque-id"), "signed identity cannot downgrade to opaque")
	foreign := newSessionOAuthAuthority(t)
	require.Error(t, validateOwnedSessionIDToken(context.Background(), cfg, sess, foreign.sign(t, a.claims("client"))), "foreign signature cannot refresh owned session")
	missingScope := a.claims("client")
	delete(missingScope, "scope")
	require.NoError(t, validateOwnedSessionIDToken(context.Background(), cfg, sess, a.sign(t, missingScope)), "OIDC identity need not carry access scopes")
}

func TestOwnedStoreRefreshRejectsDifferentCanonicalOwner(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	cfg := a.config(false)
	old := a.claims("client")
	old["exp"] = time.Now().Add(-time.Minute).Unix()
	for _, variant := range []string{"store owner", "canonical remap", "restored canonical remap", "disabled owner"} {
		t.Run(variant, func(t *testing.T) {
			store := &canceledCtxTokenStore{token: &OAuthToken{Username: "user-42", Provider: "provider", IDToken: a.sign(t, a.claims("client")), AccessToken: "fresh", ExpiresAt: time.Now().Add(time.Hour)}}
			rt, sess := runtimeRefreshHarness(a.configURL, cfg.OAuth.Client, store)
			rt.cfg = cfg
			rt.ext.cfg = cfg
			sess.Provider = "provider"
			sess.Subject = "user-123"
			sess.Tokens.IDToken = a.sign(t, old)
			switch variant {
			case "store owner":
				store.token.Username = "foreign-owner"
			case "canonical remap", "restored canonical remap":
				rt.ext.users = &testUserService{userBySubjectProvider: map[string]*User{"user-123|provider": {ID: "different-canonical"}}}
			case "disabled owner":
				rt.ext.users = &testUserService{userBySubjectProvider: map[string]*User{"user-123|provider": {ID: "user-42", Disabled: true}}}
			}
			if variant == "restored canonical remap" {
				sess.Tokens = nil
			}
			original := sess.Tokens
			result := rt.tryLoadFreshTokenFromStore(context.Background(), sess)
			require.Equal(t, tokenPreserveWithoutInjection, result.state)
			if original == nil {
				require.Nil(t, sess.Tokens)
			} else {
				require.Same(t, original, sess.Tokens)
			}
		})
	}
}

func TestOwnedStoreReloadUsesAuthoritativeResponseScopes(t *testing.T) {
	a := newSessionOAuthAuthority(t)
	cfg := a.config(false)
	idClaims := a.claims("client")
	delete(idClaims, "scope")
	store := &canceledCtxTokenStore{token: &OAuthToken{Username: "user-42", Provider: "provider", AccessToken: "opaque-access", IDToken: a.sign(t, idClaims), Scopes: []string{"read"}, ExpiresAt: time.Now().Add(time.Hour)}}
	rt, sess := runtimeRefreshHarness(a.configURL, cfg.OAuth.Client, store)
	rt.cfg = cfg
	rt.ext.cfg = cfg
	sess.Subject = "user-123"
	sess.Provider = "provider"
	sess.Scopes = []string{"read"}
	result := rt.tryLoadFreshTokenFromStore(context.Background(), sess)
	require.Equal(t, tokenAvailable, result.state)
	require.Equal(t, tokenAvailable, currentSessionOAuthToken(cfg.OAuth.Client, sess).state)
}
