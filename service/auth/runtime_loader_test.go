package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestLoadConfig_ExpandsOAuthConfigURLTemplate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_OAUTH_CONFIG_URL", "oauth_override.ref")

	config := `auth:
  enabled: true
  cookieName: agently_session
  ipHashKey: dev-hmac-salt
  oauth:
    mode: bff
    client:
      configURL: ${WORKSPACE_OAUTH_CONFIG_URL:-oauth_default.ref}
`
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "oauth_override.ref"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}

func TestLoadConfig_UsesOAuthConfigURLDefaultWhenEnvUnset(t *testing.T) {
	root := t.TempDir()

	config := `auth:
  enabled: true
  cookieName: agently_session
  ipHashKey: dev-hmac-salt
  oauth:
    mode: bff
    client:
      configURL: ${WORKSPACE_OAUTH_CONFIG_URL:-oauth_default.ref}
`
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(config), 0o644); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	cfg, err := LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg == nil || cfg.OAuth == nil || cfg.OAuth.Client == nil {
		t.Fatalf("expected oauth client config to be loaded")
	}
	if got, want := cfg.OAuth.Client.ConfigURL, "oauth_default.ref"; got != want {
		t.Fatalf("ConfigURL = %q, want %q", got, want)
	}
}

func TestOAuthVerifierConfig_UsesExplicitJWKSURL(t *testing.T) {
	cfg := &Config{
		OAuth: &OAuth{
			Client: &OAuthClient{
				JWKSURL: "https://issuer.example.test/jwks",
			},
		},
	}

	verifyCfg, err := oauthVerifierConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("oauthVerifierConfig() error = %v", err)
	}
	if verifyCfg == nil {
		t.Fatal("expected verifier config")
	}
	if got, want := verifyCfg.CertURL, "https://issuer.example.test/jwks"; got != want {
		t.Fatalf("CertURL = %q, want %q", got, want)
	}
}

func TestOAuthVerifierConfig_UsesDiscoveryURL(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != "https://issuer.example.test/.well-known/openid-configuration" {
			t.Fatalf("discovery URL = %q, want configured endpoint", got)
		}
		body, err := json.Marshal(map[string]any{
			"issuer":                                "https://issuer.example.test",
			"authorization_endpoint":                "https://issuer.example.test/authorize",
			"token_endpoint":                        "https://issuer.example.test/token",
			"jwks_uri":                              "https://issuer.example.test/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})}

	cfg := &Config{
		OAuth: &OAuth{
			Client: &OAuthClient{
				DiscoveryURL: "https://issuer.example.test/.well-known/openid-configuration",
			},
		},
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	verifyCfg, err := oauthVerifierConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("oauthVerifierConfig() error = %v", err)
	}
	if verifyCfg == nil {
		t.Fatal("expected verifier config")
	}
	if got, want := verifyCfg.CertURL, "https://issuer.example.test/jwks"; got != want {
		t.Fatalf("CertURL = %q, want %q", got, want)
	}
}

func TestOAuthVerifierConfig_IssuerUsesContextHTTPClient(t *testing.T) {
	var requests []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		return jsonResponse(req, http.StatusOK, map[string]any{
			"issuer":   "https://issuer.example.test",
			"jwks_uri": "https://issuer.example.test/jwks",
		})
	})}
	cfg := &Config{OAuth: &OAuth{Client: &OAuthClient{Issuer: "https://issuer.example.test"}}}

	verifyCfg, err := oauthVerifierConfig(context.WithValue(context.Background(), oauth2.HTTPClient, httpClient), cfg)
	if err != nil {
		t.Fatalf("oauthVerifierConfig() error = %v", err)
	}
	if verifyCfg == nil || verifyCfg.CertURL != "https://issuer.example.test/jwks" {
		t.Fatalf("verifier config = %#v, want discovered JWKS URL", verifyCfg)
	}
	if len(requests) != 1 || !strings.HasSuffix(requests[0], "/.well-known/oauth-authorization-server") {
		t.Fatalf("metadata requests = %v, want RFC 8414 metadata request", requests)
	}
}

func TestOAuthVerifierConfig_IssuerFallsBackToOpenIDDiscovery(t *testing.T) {
	var requests []string
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.URL.String())
		if strings.HasSuffix(req.URL.Path, "/.well-known/oauth-authorization-server") {
			return jsonResponse(req, http.StatusNotFound, map[string]any{})
		}
		return jsonResponse(req, http.StatusOK, map[string]any{
			"issuer":                                "https://issuer.example.test",
			"authorization_endpoint":                "https://issuer.example.test/authorize",
			"token_endpoint":                        "https://issuer.example.test/token",
			"jwks_uri":                              "https://issuer.example.test/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})}
	cfg := &Config{OAuth: &OAuth{Client: &OAuthClient{Issuer: "https://issuer.example.test"}}}

	verifyCfg, err := oauthVerifierConfig(context.WithValue(context.Background(), oauth2.HTTPClient, httpClient), cfg)
	if err != nil {
		t.Fatalf("oauthVerifierConfig() error = %v", err)
	}
	if verifyCfg == nil || verifyCfg.CertURL != "https://issuer.example.test/jwks" {
		t.Fatalf("verifier config = %#v, want discovered JWKS URL", verifyCfg)
	}
	if len(requests) != 2 || !strings.HasSuffix(requests[1], "/.well-known/openid-configuration") {
		t.Fatalf("metadata requests = %v, want OpenID fallback", requests)
	}
}

func TestOAuthMetadataHTTPClient_DefaultIsBoundedAndInjectionWins(t *testing.T) {
	if got := oauthMetadataHTTPClient(context.Background()); got.Timeout != oauthMetadataTimeout {
		t.Fatalf("default metadata timeout = %s, want %s", got.Timeout, oauthMetadataTimeout)
	}
	injected := &http.Client{}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, injected)
	if got := oauthMetadataHTTPClient(ctx); got != injected {
		t.Fatal("expected context OAuth HTTP client to be preserved")
	}
}

func jsonResponse(req *http.Request, status int, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
		Request:    req,
	}, nil
}

func TestIssuerCandidatesFromAuthURL(t *testing.T) {
	testCases := []struct {
		authURL string
		want    []string
	}{
		{authURL: "https://idp.example.test/authorize", want: []string{"https://idp.example.test"}},
		{authURL: "https://idp.example.test/oauth/authorize", want: []string{"https://idp.example.test", "https://idp.example.test/oauth"}},
		{authURL: "https://idp.example.test/oauth2/v1/authorize?client_id=abc", want: []string{"https://idp.example.test", "https://idp.example.test/oauth2/v1"}},
	}

	for _, testCase := range testCases {
		got := issuerCandidatesFromAuthURL(testCase.authURL)
		if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
			t.Fatalf("issuerCandidatesFromAuthURL(%q) = %v, want %v", testCase.authURL, got, testCase.want)
		}
	}
}

func TestDetectDBDialect(t *testing.T) {
	testCases := []struct {
		name    string
		driver  interface{}
		want    string
		wantErr string
	}{
		{name: "mysql", driver: &mysqlDriver{}, want: "mysql"},
		{name: "sqlite", driver: &sqliteDriver{}, want: "sqlite"},
		{name: "nil", driver: nil, wantErr: "nil db driver"},
		{name: "unsupported", driver: struct{}{}, wantErr: "unsupported db driver"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := detectDBDialect(testCase.driver)
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("expected error containing %q, got %v", testCase.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("expected dialect %q, got %q", testCase.want, got)
			}
		})
	}
}

type mysqlDriver struct{}

type sqliteDriver struct{}

func TestRefreshLeaseSQL(t *testing.T) {
	mysqlSQL, err := refreshLeaseSQL("mysql")
	if err != nil {
		t.Fatalf("unexpected mysql error: %v", err)
	}
	if !strings.Contains(mysqlSQL, "UTC_TIMESTAMP() + INTERVAL ? SECOND") {
		t.Fatalf("expected mysql query to use UTC_TIMESTAMP interval, got %q", mysqlSQL)
	}
	if !strings.Contains(mysqlSQL, "lease_until IS NULL OR lease_until < UTC_TIMESTAMP()") {
		t.Fatalf("expected mysql query to include NULL/expired lease check, got %q", mysqlSQL)
	}

	sqliteSQL, err := refreshLeaseSQL("sqlite")
	if err != nil {
		t.Fatalf("unexpected sqlite error: %v", err)
	}
	if !strings.Contains(sqliteSQL, "DATETIME('now', '+' || ? || ' seconds')") {
		t.Fatalf("expected sqlite query to use DATETIME now arithmetic, got %q", sqliteSQL)
	}
	if !strings.Contains(sqliteSQL, "lease_until IS NULL OR lease_until < DATETIME('now')") {
		t.Fatalf("expected sqlite query to include NULL/expired lease check, got %q", sqliteSQL)
	}

	if _, err := refreshLeaseSQL("postgres"); err == nil {
		t.Fatalf("expected unsupported dialect error")
	}
}

func TestCASPutSQL(t *testing.T) {
	mysqlSQL, err := casPutSQL("mysql")
	if err != nil {
		t.Fatalf("unexpected mysql error: %v", err)
	}
	if !strings.Contains(mysqlSQL, "updated_at = UTC_TIMESTAMP()") {
		t.Fatalf("expected mysql query to use UTC_TIMESTAMP, got %q", mysqlSQL)
	}

	sqliteSQL, err := casPutSQL("sqlite")
	if err != nil {
		t.Fatalf("unexpected sqlite error: %v", err)
	}
	if !strings.Contains(sqliteSQL, "updated_at = DATETIME('now')") {
		t.Fatalf("expected sqlite query to use DATETIME('now'), got %q", sqliteSQL)
	}

	if _, err := casPutSQL("postgres"); err == nil {
		t.Fatalf("expected unsupported dialect error")
	}
}
