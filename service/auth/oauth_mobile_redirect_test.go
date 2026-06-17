package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestAuthExtensionOAuthInitiate_UsesWebCallbackByDefault(t *testing.T) {
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/initiate", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthInitiate().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	authURL, err := url.Parse(payload["authURL"].(string))
	if err != nil {
		t.Fatalf("authURL parse error = %v", err)
	}
	want := "https://workspace.example.test/v1/api/auth/oauth/callback"
	if got := authURL.Query().Get("redirect_uri"); got != want {
		t.Fatalf("redirect_uri = %q, want %q", got, want)
	}
	if got := payload["redirectURI"]; got != want {
		t.Fatalf("payload redirectURI = %#v, want %q", got, want)
	}
}

func TestAuthExtensionOAuthInitiate_AllowsConfiguredMobileRedirect(t *testing.T) {
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL:    cfgPath,
				RedirectURIs: []string{"agently-ios://oauth/callback"},
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/initiate", strings.NewReader(`{"redirectURI":"agently-ios://oauth/callback"}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthInitiate().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	authURL, err := url.Parse(payload["authURL"].(string))
	if err != nil {
		t.Fatalf("authURL parse error = %v", err)
	}
	if got := authURL.Query().Get("redirect_uri"); got != "agently-ios://oauth/callback" {
		t.Fatalf("redirect_uri = %q, want mobile redirect", got)
	}
	if got := payload["redirectURI"]; got != "agently-ios://oauth/callback" {
		t.Fatalf("payload redirectURI = %#v, want mobile redirect", got)
	}
}

func TestAuthExtensionOAuthInitiate_RejectsUnconfiguredRedirect(t *testing.T) {
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL: cfgPath,
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/initiate", strings.NewReader(`{"redirectURI":"agently-ios://oauth/callback"}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthInitiate().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "redirectURI is not allowed") {
		t.Fatalf("body = %s, want redirect rejection", rec.Body.String())
	}
}

func TestAuthExtensionOAuthMobileInitiate_RequiresRedirectURI(t *testing.T) {
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL:    cfgPath,
				RedirectURIs: []string{"agently-ios://oauth/callback"},
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/mobile/initiate", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthMobileInitiate().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "redirectURI is required") {
		t.Fatalf("body = %s, want required redirect rejection", rec.Body.String())
	}
}

func TestAuthExtensionOAuthMobileInitiate_UsesConfiguredNativeRedirectWithPKCE(t *testing.T) {
	cfgPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL:    cfgPath,
				RedirectURIs: []string{"agently-ios://oauth/callback"},
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/mobile/initiate", strings.NewReader(`{"redirectURI":"agently-ios://oauth/callback"}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthMobileInitiate().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	authURL, err := url.Parse(payload["authURL"].(string))
	if err != nil {
		t.Fatalf("authURL parse error = %v", err)
	}
	if got := authURL.Query().Get("redirect_uri"); got != "agently-ios://oauth/callback" {
		t.Fatalf("redirect_uri = %q, want mobile redirect", got)
	}
	if got := authURL.Query().Get("code_challenge_method"); got != "S256" {
		t.Fatalf("code_challenge_method = %q, want S256", got)
	}
	if got := authURL.Query().Get("code_challenge"); got == "" {
		t.Fatalf("code_challenge is empty")
	}
	if got := payload["pkce"]; got != true {
		t.Fatalf("pkce = %#v, want true", got)
	}
	if got := payload["mobile"]; got != true {
		t.Fatalf("mobile = %#v, want true", got)
	}
}

func TestAuthExtensionOAuthMobileCallback_ExchangesWithNativeRedirectAndVerifier(t *testing.T) {
	var gotRedirectURI string
	var gotCodeVerifier string
	var gotAuthHeader string
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		gotRedirectURI = r.Form.Get("redirect_uri")
		gotCodeVerifier = r.Form.Get("code_verifier")
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	cfgPath := writeOAuthClientConfigWithTokenURL(t, tokenSrv.URL)
	ext := newAuthExtension(&Config{
		CookieName:   "agently_session",
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth: &OAuth{
			Mode: "bff",
			Client: &OAuthClient{
				ConfigURL:    cfgPath,
				RedirectURIs: []string{"agently-ios://oauth/callback"},
			},
		},
	}, NewManager(0, nil), "", nil, nil)
	state, err := encryptOAuthState(context.Background(), cfgPath, oauthStatePayload{
		CodeVerifier: "verifier-123",
		RedirectURI:  "agently-ios://oauth/callback",
	})
	if err != nil {
		t.Fatalf("encryptOAuthState() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://workspace.example.test/v1/api/auth/oauth/mobile/callback", strings.NewReader(`{"code":"code-123","state":"`+state+`"}`))
	rec := httptest.NewRecorder()

	ext.handleOAuthMobileCallback().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if gotRedirectURI != "agently-ios://oauth/callback" {
		t.Fatalf("token redirect_uri = %q, want native redirect", gotRedirectURI)
	}
	if gotCodeVerifier != "verifier-123" {
		t.Fatalf("token code_verifier = %q, want stored verifier", gotCodeVerifier)
	}
	if gotAuthHeader == "" {
		t.Fatalf("token request Authorization header is empty")
	}
}

func writeOAuthClientConfig(t *testing.T) string {
	return writeOAuthClientConfigWithTokenURL(t, "https://idp.example.test/oauth/token")
}

func writeOAuthClientConfigWithTokenURL(t *testing.T, tokenURL string) string {
	t.Helper()
	path := t.TempDir() + "/oauth.json"
	body := `{
		"authURL": "https://idp.example.test/oauth/authorize",
		"tokenURL": "` + tokenURL + `",
		"clientID": "client-id",
		"clientSecret": "secret",
		"scopes": ["openid", "profile", "email"]
	}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	return path
}
