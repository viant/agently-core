package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	token "github.com/viant/agently-core/internal/auth/token"
	"golang.org/x/oauth2"
)

type brokerStoreStub struct {
	token       *OAuthToken
	getUsername string
	getProvider string
	putCalls    int
	deleteCalls int
}

func (s *brokerStoreStub) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	s.getUsername = username
	s.getProvider = provider
	return s.token, nil
}
func (s *brokerStoreStub) Put(ctx context.Context, token *OAuthToken) error {
	s.putCalls++
	return nil
}
func (s *brokerStoreStub) Delete(ctx context.Context, username, provider string) error {
	s.deleteCalls++
	s.token = nil
	return nil
}
func (s *brokerStoreStub) TryAcquireRefreshLease(ctx context.Context, username, provider, owner string, ttl time.Duration) (int64, bool, error) {
	return 0, false, nil
}
func (s *brokerStoreStub) ReleaseRefreshLease(ctx context.Context, username, provider, owner string) error {
	return nil
}
func (s *brokerStoreStub) CASPut(ctx context.Context, token *OAuthToken, expectedVersion int64, owner string) (bool, error) {
	return false, nil
}

func tokenEndpointContext(t *testing.T, inspect func(url.Values)) context.Context {
	t.Helper()
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != "https://token.example.test/oauth/token" {
			t.Fatalf("token URL = %q, want token endpoint", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		values, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatalf("ParseQuery() error = %v", err)
		}
		if inspect != nil {
			inspect(values)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`)),
			Request:    req,
		}, nil
	})}
	return context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
}

func writeBrokerOAuthConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "oauth.json")
	cfgBody, _ := json.Marshal(map[string]any{
		"authURL":      "https://idp.example.com/auth",
		"tokenURL":     "https://token.example.test/oauth/token",
		"clientID":     "client-1",
		"clientSecret": "secret-1",
		"redirectURL":  "http://localhost/callback",
		"scopes":       []string{"openid"},
	})
	if err := os.WriteFile(cfgPath, cfgBody, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return cfgPath
}

func TestOAuthRefreshBroker_Refresh_PreservesStoredIDTokenWhenRefreshResponseOmitsIt(t *testing.T) {
	ctx := tokenEndpointContext(t, func(values url.Values) {
		if got := strings.TrimSpace(values.Get("refresh_token")); got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want %q", got, "refresh-1")
		}
	})

	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		store: &brokerStoreStub{token: &OAuthToken{
			Username:     "user-1",
			Provider:     "oauth",
			AccessToken:  "old-access",
			IDToken:      "stored-id-token",
			RefreshToken: "refresh-1",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}},
	}

	got, err := broker.Refresh(ctx, token.Key{Subject: "user-1", Provider: "oauth"}, "refresh-1")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Refresh() returned nil token")
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("AccessToken = %q, want %q", got.AccessToken, "new-access")
	}
	if got.RefreshToken != "new-refresh" {
		t.Fatalf("RefreshToken = %q, want %q", got.RefreshToken, "new-refresh")
	}
	if got.IDToken != "stored-id-token" {
		t.Fatalf("IDToken = %q, want %q", got.IDToken, "stored-id-token")
	}
}

func TestOAuthRefreshBroker_Refresh_UsesCanonicalTokenOwner(t *testing.T) {
	ctx := tokenEndpointContext(t, nil)

	rawStore := &brokerStoreStub{token: &OAuthToken{
		Username:     "user-42",
		Provider:     "oauth",
		AccessToken:  "old-access",
		IDToken:      "stored-id-token",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}}
	users := &testUserService{userBySubjectProvider: map[string]*User{
		"user-sub-123|oauth": {ID: "user-42", Username: "localuser"},
	}}
	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		store:     &canonicalTokenStore{inner: rawStore, users: users},
	}

	got, err := broker.Refresh(ctx, token.Key{Subject: "user-sub-123", Provider: "oauth"}, "refresh-1")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got == nil {
		t.Fatalf("Refresh() returned nil token")
	}
	if rawStore.getUsername != "user-42" {
		t.Fatalf("stored token lookup username = %q, want canonical user ID %q", rawStore.getUsername, "user-42")
	}
	if rawStore.getProvider != "oauth" {
		t.Fatalf("stored token lookup provider = %q, want %q", rawStore.getProvider, "oauth")
	}
	if got.IDToken != "stored-id-token" {
		t.Fatalf("IDToken = %q, want %q", got.IDToken, "stored-id-token")
	}
}

func TestOAuthRefreshBroker_Refresh_SendsStoredScopes(t *testing.T) {
	scopeToken := fakeJWTWithClaims(t, map[string]any{
		"scope": "XXX_WEBUI openid",
	})
	ctx := tokenEndpointContext(t, func(values url.Values) {
		if got := strings.TrimSpace(values.Get("scope")); got != "XXX_WEBUI openid" {
			t.Fatalf("scope = %q, want %q", got, "XXX_WEBUI openid")
		}
	})

	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		store: &brokerStoreStub{token: &OAuthToken{
			Username:     "user-1",
			Provider:     "oauth",
			AccessToken:  scopeToken,
			IDToken:      scopeToken,
			RefreshToken: "refresh-1",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}},
	}

	if _, err := broker.Refresh(ctx, token.Key{Subject: "user-1", Provider: "oauth"}, "refresh-1"); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
}

func TestOAuthRefreshBroker_Refresh_DropsExpiredStoredIDToken(t *testing.T) {
	expiredIDToken := fakeJWTWithClaims(t, map[string]any{
		"exp":   time.Now().Add(-time.Minute).Unix(),
		"scope": "ROLE_STEWARD_WEB openid",
	})
	ctx := tokenEndpointContext(t, func(values url.Values) {
		if got := strings.TrimSpace(values.Get("scope")); got != "ROLE_STEWARD_WEB openid" {
			t.Fatalf("scope = %q, want persisted Steward scope", got)
		}
	})
	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		client: &OAuthClient{
			Scopes:      []string{"openid", "profile", "email"},
			WebUIScopes: []string{"ROLE_STEWARD_WEB"},
		},
		store: &brokerStoreStub{token: &OAuthToken{
			Username:     "user-1",
			Provider:     "oauth",
			AccessToken:  expiredIDToken,
			IDToken:      expiredIDToken,
			RefreshToken: "refresh-1",
			ExpiresAt:    time.Now().Add(-time.Minute),
		}},
	}

	got, err := broker.Refresh(ctx, token.Key{Subject: "user-1", Provider: "oauth"}, "refresh-1")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if got == nil {
		t.Fatal("Refresh() returned nil token")
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("AccessToken = %q, want refreshed access token", got.AccessToken)
	}
	if got.IDToken != "" {
		t.Fatalf("IDToken = %q, want expired ID token removed", got.IDToken)
	}
}

func TestOAuthRefreshBroker_Refresh_RejectsUnderScopedCandidate(t *testing.T) {
	underScoped := fakeJWTWithClaims(t, map[string]any{"scope": "openid"})
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"access_token":  underScoped,
			"refresh_token": "candidate-refresh",
			"expires_in":    3600,
			"scope":         "openid",
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})})
	store := &brokerStoreStub{token: &OAuthToken{
		Username:     "user-1",
		Provider:     "oauth",
		AccessToken:  "old-access",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}}
	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		client: &OAuthClient{
			Scopes: []string{"openid", "ROLE_STEWARD_WEB"},
		},
		store: store,
	}

	got, err := broker.Refresh(ctx, token.Key{Subject: "user-1", Provider: "oauth"}, "refresh-1")
	if err == nil {
		t.Fatalf("Refresh() error = nil, token=%#v", got)
	}
	if got != nil {
		t.Fatalf("scope-failed candidate returned: %#v", got)
	}
	if store.putCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("scope-failed broker mutated store: put=%d delete=%d", store.putCalls, store.deleteCalls)
	}
}

func TestOAuthRefreshBroker_InvalidGrantClearsSchedulerStoreAndDoesNotInject(t *testing.T) {
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)),
			Request:    req,
		}, nil
	})})
	store := &brokerStoreStub{token: &OAuthToken{
		Username:     "user-1",
		Provider:     "oauth",
		AccessToken:  "expired-access",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}}
	broker := &oauthRefreshBroker{
		configURL: writeBrokerOAuthConfig(t),
		store:     store,
	}
	manager := token.NewManager(
		token.WithBroker(broker),
		token.WithTokenStore(NewTokenStoreAdapter(store, nil)),
		token.WithInstanceID(""),
	)

	next, err := manager.EnsureTokens(ctx, token.Key{Subject: "user-1", Provider: "oauth"})
	if err != nil {
		t.Fatalf("EnsureTokens() error = %v", err)
	}
	if got := iauth.TokensFromContext(next); got != nil {
		t.Fatalf("expired scheduler token was injected: %#v", got)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("scheduler invalid_grant Delete calls = %d, want 1", store.deleteCalls)
	}
	if store.token != nil {
		t.Fatalf("scheduler stored credential was not cleared: %#v", store.token)
	}
	if _, err := manager.EnsureTokens(ctx, token.Key{Subject: "user-1", Provider: "oauth"}); err != nil {
		t.Fatalf("second EnsureTokens() error = %v", err)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("scheduler invalid_grant Delete calls after cached miss = %d, want 1", store.deleteCalls)
	}
}

func TestOAuthRefreshBroker_NonInvalidGrantPreservesSchedulerStore(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		calls int
	}{
		{name: "invalid_client", code: "invalid_client", calls: 2},
		{name: "invalid_request", code: "invalid_request", calls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oauthRefreshAuthStyles.Delete(oauthAuthStyleCacheKey{
				tokenURL: "https://token.example.test/oauth/token",
				clientID: "client-1",
			})
			requests := 0
			ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     "400 Bad Request",
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"error":"` + test.code + `"}`)),
					Request:    req,
				}, nil
			})})
			stored := &OAuthToken{
				Username:     "user-1",
				Provider:     "oauth",
				AccessToken:  "expired-access",
				RefreshToken: "refresh-1",
				ExpiresAt:    time.Now().Add(-time.Minute),
			}
			store := &brokerStoreStub{token: stored}
			manager := token.NewManager(
				token.WithBroker(&oauthRefreshBroker{
					configURL: writeBrokerOAuthConfig(t),
					store:     store,
				}),
				token.WithTokenStore(NewTokenStoreAdapter(store, nil)),
				token.WithInstanceID(""),
			)

			next, err := manager.EnsureTokens(ctx, token.Key{Subject: "user-1", Provider: "oauth"})
			if err != nil {
				t.Fatalf("EnsureTokens() error = %v", err)
			}
			if got := iauth.TokensFromContext(next); got != nil || iauth.Bearer(next) != "" {
				t.Fatalf("failed refresh injected stale scheduler credentials: %#v", got)
			}
			if store.deleteCalls != 0 {
				t.Fatalf("Delete calls = %d, want 0", store.deleteCalls)
			}
			if store.token != stored {
				t.Fatalf("stored credential changed: %#v", store.token)
			}
			if requests != test.calls {
				t.Fatalf("token endpoint calls = %d, want %d", requests, test.calls)
			}
		})
	}
}
