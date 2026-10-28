package auth

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestOAuthLoginLogging_InitiateAndCallbackBoundariesWithoutSecrets(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "")
	output := captureStandardLog(t)
	configPath := writeOAuthClientConfig(t)
	ext := newAuthExtension(&Config{
		RedirectPath: "/v1/api/auth/oauth/callback",
		OAuth:        &OAuth{Client: &OAuthClient{ConfigURL: configPath}},
	}, NewManager(0, nil), "", nil, nil)

	initiate := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oauth/initiate", strings.NewReader(`{}`))
	initiateResult := httptest.NewRecorder()
	ext.handleOAuthInitiate().ServeHTTP(initiateResult, initiate)
	if initiateResult.Code != http.StatusOK {
		t.Fatalf("initiate status = %d, body=%s", initiateResult.Code, initiateResult.Body.String())
	}
	login := httptest.NewRequest(http.MethodGet, "/v1/api/auth/idp/login?returnURL=%2F", nil)
	loginResult := httptest.NewRecorder()
	ext.handleIDPLogin().ServeHTTP(loginResult, login)
	if loginResult.Code != http.StatusTemporaryRedirect {
		t.Fatalf("IDP login status = %d, want %d", loginResult.Code, http.StatusTemporaryRedirect)
	}

	callback := httptest.NewRequest(http.MethodGet, "/v1/api/auth/oauth/callback?state=CANARY_STATE", nil)
	callbackResult := httptest.NewRecorder()
	ext.handleOAuthCallback().ServeHTTP(callbackResult, callback)
	if callbackResult.Code != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want %d", callbackResult.Code, http.StatusBadRequest)
	}

	logs := output.String()
	for _, want := range []string{
		`op="oauth_login"`,
		`source="oauth_initiate"`,
		`source="idp_login"`,
		`source="oauth_callback"`,
		`action="authorization_url_issued"`,
		`reason="code_or_state_missing"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("missing %s in log: %s", want, logs)
		}
	}
	if strings.Contains(logs, "CANARY_STATE") || strings.Contains(logs, "?state=") {
		t.Fatalf("oauth state leaked into log: %s", logs)
	}
}

func TestOAuthLoginLogging_ExchangeKeepsResponseBodyAndURLQueryPrivate(t *testing.T) {
	output := captureStandardLog(t)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	endpoint := "https://idp.example.test/oauth/token?secret=CANARY_QUERY"
	logOAuthLogin(r, "oauth", "oauth_callback", "failed", "token_exchange", endpoint, 0, &oauth2.RetrieveError{
		Response:  &http.Response{StatusCode: http.StatusUnauthorized},
		Body:      []byte("CANARY_RESPONSE_BODY"),
		ErrorCode: "invalid_client",
	})
	logOAuthLogin(r, "oauth", "oauth_callback", "failed", "token_exchange", endpoint, 0, &url.Error{
		Op:  "Post",
		URL: endpoint,
		Err: &net.DNSError{Err: "no such host", Name: "idp.example.test"},
	})
	logOAuthLogin(r, "oauth", "oauth_callback", "failed", "token_exchange", endpoint, 0, &oauth2.RetrieveError{
		Response:  &http.Response{StatusCode: http.StatusUnauthorized},
		ErrorCode: "CANARY_UNKNOWN_OAUTH_SECRET",
	})
	logOAuthLogin(r, "oauth", "oauth_callback", "failed", "token_exchange", endpoint, 0, errors.New("CANARY_UNSTRUCTURED_SECRET"))

	logs := output.String()
	for _, want := range []string{
		`endpoint_host="idp.example.test"`,
		`endpoint_path="/oauth/token"`,
		`http_status=401`,
		`oauth_error="invalid_client"`,
		`classification="transport"`,
		`reason="token_exchange_dns"`,
		`oauth_error="other"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("missing %s in log: %s", want, logs)
		}
	}
	for _, secret := range []string{"CANARY_QUERY", "CANARY_RESPONSE_BODY", "CANARY_UNKNOWN_OAUTH_SECRET", "CANARY_UNSTRUCTURED_SECRET"} {
		if strings.Contains(strings.ToLower(logs), strings.ToLower(secret)) {
			t.Fatalf("log leaked %s: %s", secret, logs)
		}
	}
}
