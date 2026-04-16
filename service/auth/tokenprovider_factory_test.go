package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	token "github.com/viant/agently-core/internal/auth/token"
)

type brokerStoreStub struct {
	token *OAuthToken
}

func (s *brokerStoreStub) Get(ctx context.Context, username, provider string) (*OAuthToken, error) {
	return s.token, nil
}
func (s *brokerStoreStub) Put(ctx context.Context, token *OAuthToken) error { return nil }
func (s *brokerStoreStub) Delete(ctx context.Context, username, provider string) error {
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

func TestOAuthRefreshBroker_Refresh_PreservesStoredIDTokenWhenRefreshResponseOmitsIt(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		if got := strings.TrimSpace(r.FormValue("refresh_token")); got != "refresh-1" {
			t.Fatalf("refresh_token = %q, want %q", got, "refresh-1")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	root := t.TempDir()
	cfgPath := filepath.Join(root, "oauth.json")
	cfgBody, _ := json.Marshal(map[string]any{
		"authURL":      "https://idp.example.com/auth",
		"tokenURL":     tokenSrv.URL,
		"clientID":     "client-1",
		"clientSecret": "secret-1",
		"redirectURL":  "http://localhost/callback",
		"scopes":       []string{"openid"},
	})
	if err := os.WriteFile(cfgPath, cfgBody, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	broker := &oauthRefreshBroker{
		configURL: cfgPath,
		store: &brokerStoreStub{token: &OAuthToken{
			Username:     "user-1",
			Provider:     "oauth",
			AccessToken:  "old-access",
			IDToken:      "stored-id-token",
			RefreshToken: "refresh-1",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}},
	}

	got, err := broker.Refresh(context.Background(), token.Key{Subject: "user-1", Provider: "oauth"}, "refresh-1")
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
