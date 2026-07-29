package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/scy"
	"github.com/viant/scy/cred"
	"golang.org/x/oauth2"
)

type oauthRefreshRoundTripFunc func(*http.Request) (*http.Response, error)

func (f oauthRefreshRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func oauthRefreshContext(fn oauthRefreshRoundTripFunc) context.Context {
	return context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fn})
}

func oauthRefreshResponse(req *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func oauthRefreshConfig(tokenURL string, style oauth2.AuthStyle) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client+id@example.test",
		ClientSecret: "secret:/+?&=",
		Endpoint: oauth2.Endpoint{
			TokenURL:  tokenURL,
			AuthStyle: style,
		},
	}
}

func TestOAuthRefreshResource_UsesNonClientAudience(t *testing.T) {
	clientID := "scheduler-client"
	resource := "https://api.example.test"
	multiAudience := fakeJWTWithClaims(t, map[string]any{
		"aud": []any{clientID, resource},
	})
	if got := oauthRefreshResource(nil, &OAuthToken{AccessToken: multiAudience}, clientID); got != resource {
		t.Fatalf("oauthRefreshResource() = %q, want %q", got, resource)
	}
}

func TestOAuthRefreshResource_ContinuesAcrossCandidateTokens(t *testing.T) {
	clientID := "scheduler-client"
	resource := "https://api.example.test"
	clientAudience := fakeJWTWithClaims(t, map[string]any{"aud": clientID})
	resourceAudience := fakeJWTWithClaims(t, map[string]any{"aud": resource})
	stored := &OAuthToken{
		RefreshToken: clientAudience,
		AccessToken:  resourceAudience,
	}
	if got := oauthRefreshResource(nil, stored, clientID); got != resource {
		t.Fatalf("oauthRefreshResource() = %q, want %q", got, resource)
	}
}

func readRefreshRequest(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("parse request body: %v", err)
	}
	return values
}

func TestRefreshOAuthToken_ExplicitAuthStylesAndBasicEscaping(t *testing.T) {
	tests := []struct {
		name  string
		style oauth2.AuthStyle
		check func(*testing.T, *http.Request, url.Values)
	}{
		{
			name:  "header",
			style: oauth2.AuthStyleInHeader,
			check: func(t *testing.T, req *http.Request, values url.Values) {
				gotUser, gotPassword, ok := req.BasicAuth()
				if !ok {
					t.Fatal("missing Basic authorization")
				}
				if gotUser != url.QueryEscape("client+id@example.test") {
					t.Fatalf("Basic username = %q, want oauth2 query escaping", gotUser)
				}
				if gotPassword != url.QueryEscape("secret:/+?&=") {
					t.Fatalf("Basic password = %q, want oauth2 query escaping", gotPassword)
				}
				raw := strings.TrimPrefix(req.Header.Get("Authorization"), "Basic ")
				if _, err := base64.StdEncoding.DecodeString(raw); err != nil {
					t.Fatalf("Basic payload is not base64: %v", err)
				}
				if values.Get("client_id") != "" || values.Get("client_secret") != "" {
					t.Fatalf("header auth leaked credentials into form: %v", values)
				}
			},
		},
		{
			name:  "params",
			style: oauth2.AuthStyleInParams,
			check: func(t *testing.T, req *http.Request, values url.Values) {
				if got := req.Header.Get("Authorization"); got != "" {
					t.Fatalf("params auth Authorization = %q, want empty", got)
				}
				if got := values.Get("client_id"); got != "client+id@example.test" {
					t.Fatalf("client_id = %q", got)
				}
				if got := values.Get("client_secret"); got != "secret:/+?&=" {
					t.Fatalf("client_secret = %q", got)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
				values := readRefreshRequest(t, req)
				test.check(t, req, values)
				return oauthRefreshResponse(req, http.StatusOK, "application/json",
					`{"access_token":"access","token_type":"Bearer","expires_in":3600}`), nil
			})
			token, err := refreshOAuthToken(ctx, oauthRefreshConfig("https://tokens.example.test/"+test.name, test.style),
				&oauth2.Token{RefreshToken: "old-refresh"}, nil, "")
			if err != nil {
				t.Fatalf("refreshOAuthToken() error = %v", err)
			}
			if token.AccessToken != "access" || token.RefreshToken != "old-refresh" {
				t.Fatalf("token = %#v", token)
			}
		})
	}
}

func TestRefreshOAuthToken_AutoFallbackCachesParams(t *testing.T) {
	tokenURL := "https://tokens.example.test/auto-cache"
	config := oauthRefreshConfig(tokenURL, oauth2.AuthStyleAutoDetect)
	oauthRefreshAuthStyles.Delete(oauthAuthStyleCacheKey{tokenURL: tokenURL, clientID: config.ClientID})
	var calls atomic.Int32
	ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
		call := calls.Add(1)
		values := readRefreshRequest(t, req)
		if call == 1 {
			if _, _, ok := req.BasicAuth(); !ok {
				t.Fatal("first auto request must use header auth")
			}
			return oauthRefreshResponse(req, http.StatusUnauthorized, "application/json",
				`{"error":"invalid_client","error_description":"header rejected"}`), nil
		}
		if req.Header.Get("Authorization") != "" {
			t.Fatalf("params request unexpectedly has Authorization: %q", req.Header.Get("Authorization"))
		}
		if values.Get("client_id") != config.ClientID || values.Get("client_secret") != config.ClientSecret {
			t.Fatalf("params request credentials = %v", values)
		}
		return oauthRefreshResponse(req, http.StatusOK, "application/json", `{"access_token":"access"}`), nil
	})

	for i := 0; i < 2; i++ {
		if _, err := refreshOAuthToken(ctx, config, &oauth2.Token{RefreshToken: "refresh"}, []string{"scope-a"}, "resource-a"); err != nil {
			t.Fatalf("refresh %d error = %v", i+1, err)
		}
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("HTTP calls = %d, want header+params then cached params", got)
	}
}

func TestRefreshOAuthToken_AutoDoesNotRetryUnsafeFailures(t *testing.T) {
	tests := []struct {
		name      string
		transport func(*http.Request) (*http.Response, error)
	}{
		{
			name: "transport",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network unavailable")
			},
		},
		{
			name: "timeout",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, context.DeadlineExceeded
			},
		},
		{
			name: "server",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusServiceUnavailable, "application/json", `{"error":"server_error"}`), nil
			},
		},
		{
			name: "server invalid client",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusInternalServerError, "application/json", `{"error":"invalid_client"}`), nil
			},
		},
		{
			name: "invalid grant",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusBadRequest, "application/json", `{"error":"invalid_grant"}`), nil
			},
		},
		{
			name: "other grant error",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusBadRequest, "application/json", `{"error":"invalid_request"}`), nil
			},
		},
		{
			name: "parse",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusOK, "application/json", `not-json`), nil
			},
		},
		{
			name: "client error parse",
			transport: func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusBadRequest, "application/json", `not-json`), nil
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokenURL := "https://tokens.example.test/no-retry/" + url.PathEscape(test.name)
			config := oauthRefreshConfig(tokenURL, oauth2.AuthStyleAutoDetect)
			oauthRefreshAuthStyles.Delete(oauthAuthStyleCacheKey{tokenURL: tokenURL, clientID: config.ClientID})
			var calls atomic.Int32
			ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				return test.transport(req)
			})
			if _, err := refreshOAuthToken(ctx, config, &oauth2.Token{RefreshToken: "refresh"}, nil, ""); err == nil {
				t.Fatal("refreshOAuthToken() error = nil")
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("HTTP calls = %d, want 1", got)
			}
		})
	}
}

func TestRefreshOAuthToken_ResponseFormatsErrorsAndExtras(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		check       func(*testing.T, *oauth2.Token)
	}{
		{
			name:        "json",
			contentType: "application/json",
			body:        `{"access_token":"json-access","token_type":"Bearer","expires_in":"3600","id_token":"json-id","scope":"openid role"}`,
			check: func(t *testing.T, token *oauth2.Token) {
				if token.AccessToken != "json-access" || token.RefreshToken != "old-refresh" {
					t.Fatalf("JSON token = %#v", token)
				}
				if token.Extra("id_token") != "json-id" || token.Extra("scope") != "openid role" {
					t.Fatalf("JSON extras id=%v scope=%v", token.Extra("id_token"), token.Extra("scope"))
				}
				if time.Until(token.Expiry) < 59*time.Minute {
					t.Fatalf("JSON expiry = %v", token.Expiry)
				}
			},
		},
		{
			name:        "form",
			contentType: "application/x-www-form-urlencoded",
			body:        `access_token=form-access&token_type=Bearer&expires_in=120&id_token=form-id&scope=openid+role`,
			check: func(t *testing.T, token *oauth2.Token) {
				if token.AccessToken != "form-access" || token.RefreshToken != "old-refresh" {
					t.Fatalf("form token = %#v", token)
				}
				if token.Extra("id_token") != "form-id" || token.Extra("scope") != "openid role" {
					t.Fatalf("form extras id=%v scope=%v", token.Extra("id_token"), token.Extra("scope"))
				}
			},
		},
		{
			name:        "text",
			contentType: "text/plain",
			body:        `access_token=text-access&scope=openid`,
			check: func(t *testing.T, token *oauth2.Token) {
				if token.AccessToken != "text-access" {
					t.Fatalf("text token = %#v", token)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusOK, test.contentType, test.body), nil
			})
			token, err := refreshOAuthToken(ctx, oauthRefreshConfig("https://tokens.example.test/format/"+test.name, oauth2.AuthStyleInHeader),
				&oauth2.Token{RefreshToken: "old-refresh"}, nil, "")
			if err != nil {
				t.Fatalf("refreshOAuthToken() error = %v", err)
			}
			test.check(t, token)
		})
	}

	t.Run("2xx oauth error", func(t *testing.T) {
		ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
			return oauthRefreshResponse(req, http.StatusOK, "application/json",
				`{"error":"invalid_grant","error_description":"revoked","error_uri":"https://docs.example.test/error?secret=canary"}`), nil
		})
		_, err := refreshOAuthToken(ctx, oauthRefreshConfig("https://tokens.example.test/2xx-error", oauth2.AuthStyleInHeader),
			&oauth2.Token{RefreshToken: "old-refresh"}, nil, "")
		var retrieval *oauth2.RetrieveError
		if !errors.As(err, &retrieval) {
			t.Fatalf("error = %T %v, want RetrieveError", err, err)
		}
		if retrieval.Response.StatusCode != http.StatusOK || retrieval.ErrorCode != "invalid_grant" ||
			retrieval.ErrorDescription != "revoked" || !strings.Contains(retrieval.ErrorURI, "docs.example.test") {
			t.Fatalf("RetrieveError = %#v", retrieval)
		}
	})

	t.Run("form oauth error", func(t *testing.T) {
		ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
			return oauthRefreshResponse(req, http.StatusBadRequest, "application/x-www-form-urlencoded",
				`error=invalid_request&error_description=malformed&error_uri=https%3A%2F%2Fdocs.example.test%2Ferror`), nil
		})
		_, err := refreshOAuthToken(ctx, oauthRefreshConfig("https://tokens.example.test/form-error", oauth2.AuthStyleInHeader),
			&oauth2.Token{RefreshToken: "old-refresh"}, nil, "")
		var retrieval *oauth2.RetrieveError
		if !errors.As(err, &retrieval) {
			t.Fatalf("error = %T %v, want RetrieveError", err, err)
		}
		if retrieval.ErrorCode != "invalid_request" || retrieval.ErrorDescription != "malformed" ||
			retrieval.ErrorURI != "https://docs.example.test/error" {
			t.Fatalf("RetrieveError = %#v", retrieval)
		}
	})
}

func TestRefreshOAuthToken_BlankOrMissingResponseScopeUsesFallback(t *testing.T) {
	expected := []string{"openid", "profile"}
	tests := []struct {
		name        string
		contentType string
		body        string
		wantAccess  string
	}{
		{
			name:        "json explicit empty scope",
			contentType: "application/json",
			body:        `{"access_token":"json-empty-scope-access","token_type":"Bearer","scope":""}`,
			wantAccess:  "json-empty-scope-access",
		},
		{
			name:        "form missing scope",
			contentType: "application/x-www-form-urlencoded",
			body:        `access_token=form-missing-scope-access&token_type=Bearer`,
			wantAccess:  "form-missing-scope-access",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
				return oauthRefreshResponse(req, http.StatusOK, test.contentType, test.body), nil
			})
			token, err := refreshOAuthToken(ctx,
				oauthRefreshConfig("https://tokens.example.test/scope-fallback/"+url.PathEscape(test.name), oauth2.AuthStyleInHeader),
				&oauth2.Token{RefreshToken: "preserved-refresh-token"},
				expected,
				"",
			)
			if err != nil {
				t.Fatalf("refreshOAuthToken() error = %v", err)
			}
			if token.AccessToken != test.wantAccess {
				t.Fatalf("AccessToken = %q, want %q", token.AccessToken, test.wantAccess)
			}
			if token.RefreshToken != "preserved-refresh-token" {
				t.Fatalf("RefreshToken = %q, want preserved refresh token", token.RefreshToken)
			}
			if raw, ok := token.Extra("scope").(string); !ok || raw != "" {
				t.Fatalf("scope extra = %#v, want blank string", token.Extra("scope"))
			}
			if err := validateRefreshedOAuthScopes(nil, expected, token, ""); err != nil {
				t.Fatalf("validateRefreshedOAuthScopes() error = %v", err)
			}
		})
	}
}

func TestRefreshOAuthToken_PreservesScopeAndResourceConstruction(t *testing.T) {
	ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
		values := readRefreshRequest(t, req)
		if got := values.Get("grant_type"); got != "refresh_token" {
			t.Fatalf("grant_type = %q", got)
		}
		if got := values.Get("refresh_token"); got != "refresh value" {
			t.Fatalf("refresh_token = %q", got)
		}
		if got := values.Get("scope"); got != "scope-b scope-a" {
			t.Fatalf("scope = %q, want normalized order-preserving construction", got)
		}
		if got := values.Get("resource"); got != "api://resource/value" {
			t.Fatalf("resource = %q", got)
		}
		return oauthRefreshResponse(req, http.StatusOK, "application/json", `{"access_token":"access"}`), nil
	})
	if _, err := refreshOAuthToken(ctx,
		oauthRefreshConfig("https://tokens.example.test/exact", oauth2.AuthStyleInHeader),
		&oauth2.Token{RefreshToken: "refresh value"},
		[]string{" scope-b ", "scope-a", "scope-b"},
		" api://resource/value ",
	); err != nil {
		t.Fatalf("refreshOAuthToken() error = %v", err)
	}
}

func TestLoadOAuthClientConfig_FileAuthStyle(t *testing.T) {
	for _, test := range []struct {
		value string
		want  oauth2.AuthStyle
	}{
		{value: "", want: oauth2.AuthStyleAutoDetect},
		{value: "auto", want: oauth2.AuthStyleAutoDetect},
		{value: "header", want: oauth2.AuthStyleInHeader},
		{value: "params", want: oauth2.AuthStyleInParams},
	} {
		t.Run(firstNonEmpty(test.value, "absent"), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "oauth.json")
			authStyle := ""
			if test.value != "" {
				authStyle = `,"authStyle":"` + test.value + `"`
			}
			body := `{"authURL":"https://idp.example.test/auth","tokenURL":"https://idp.example.test/token","clientID":"client"` + authStyle + `}`
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			config, err := loadOAuthClientConfig(context.Background(), path)
			if err != nil {
				t.Fatalf("loadOAuthClientConfig() error = %v", err)
			}
			if config.Endpoint.AuthStyle != test.want {
				t.Fatalf("AuthStyle = %d, want %d", config.Endpoint.AuthStyle, test.want)
			}
		})
	}
}

func TestLoadOAuthClientConfig_SCYPreservesConfig(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "oauth.enc")
	resource := scy.NewResource(&cred.Oauth2Config{}, path, "blowfish://default")
	secret := scy.NewSecret(&cred.Oauth2Config{
		Config: oauth2.Config{
			ClientID:     "scy-client",
			ClientSecret: "scy-secret",
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://idp.example.test/auth",
				TokenURL:  "https://idp.example.test/token",
				AuthStyle: oauth2.AuthStyleInHeader,
			},
			RedirectURL: "https://app.example.test/callback",
			Scopes:      []string{"openid", "profile"},
		},
	}, resource)
	if err := scy.New().Store(ctx, secret); err != nil {
		t.Fatalf("store SCY oauth config: %v", err)
	}

	fileURL := (&url.URL{Scheme: "file", Path: path}).String()
	for _, test := range []struct {
		name      string
		configURL string
	}{
		{name: "absolute path", configURL: path + "|blowfish://default"},
		{name: "file URL", configURL: fileURL + "|blowfish://default"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := loadOAuthClientConfig(ctx, test.configURL)
			if err != nil {
				t.Fatalf("loadOAuthClientConfig() error = %v", err)
			}
			if config.ClientID != "scy-client" {
				t.Fatalf("ClientID = %q, want scy-client", config.ClientID)
			}
			if config.ClientSecret != "scy-secret" {
				t.Fatalf("ClientSecret was not deciphered")
			}
			if config.Endpoint.AuthURL != "https://idp.example.test/auth" ||
				config.Endpoint.TokenURL != "https://idp.example.test/token" {
				t.Fatalf("Endpoint = %#v, want stored SCY endpoint", config.Endpoint)
			}
			if config.Endpoint.AuthStyle != oauth2.AuthStyleInHeader {
				t.Fatalf("AuthStyle = %d, want %d", config.Endpoint.AuthStyle, oauth2.AuthStyleInHeader)
			}
			if config.RedirectURL != "https://app.example.test/callback" {
				t.Fatalf("RedirectURL = %q, want stored SCY redirect URL", config.RedirectURL)
			}
			if len(config.Scopes) != 2 || config.Scopes[0] != "openid" || config.Scopes[1] != "profile" {
				t.Fatalf("Scopes = %v, want [openid profile]", config.Scopes)
			}
		})
	}
}
