package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testOAuthConfigURL = "oauth_config_test.enc|secret://test"

func TestAuthExtensionHandleOAuthConfig_ExposesUsePopupLogin(t *testing.T) {
	cfg := &Config{
		OAuth: &OAuth{
			Mode:          "bff",
			UsePopupLogin: true,
			Client: &OAuthClient{
				ConfigURL:      testOAuthConfigURL,
				WebUIScopes:    []string{"XXX_WEBUI"},
				MobileUIScopes: []string{"XXX_MOBILEUI"},
				CLIScopes:      []string{"XXX_CLI"},
			},
		},
	}
	ext := newAuthExtension(cfg, NewManager(0, nil), "", nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/oauth/config", nil)
	rec := httptest.NewRecorder()

	ext.handleOAuthConfig().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if got, ok := payload["usePopupLogin"].(bool); !ok || !got {
		t.Fatalf("usePopupLogin = %#v, want true", payload["usePopupLogin"])
	}
	if got, ok := payload["redirectSameTab"].(bool); !ok || got {
		t.Fatalf("redirectSameTab = %#v, want false", payload["redirectSameTab"])
	}
	if got := payload["configURL"]; got != testOAuthConfigURL {
		t.Fatalf("configURL = %#v, want encrypted config URL", got)
	}
	if got, ok := payload["scopes"].([]any); !ok || len(got) != 0 {
		t.Fatalf("scopes = %#v, want empty array", payload["scopes"])
	}
	if got, ok := payload["webUIScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_WEBUI" {
		t.Fatalf("webUIScopes = %#v, want [XXX_WEBUI]", payload["webUIScopes"])
	}
	if got, ok := payload["mobileUIScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_MOBILEUI" {
		t.Fatalf("mobileUIScopes = %#v, want [XXX_MOBILEUI]", payload["mobileUIScopes"])
	}
	if got, ok := payload["cliScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_CLI" {
		t.Fatalf("cliScopes = %#v, want [XXX_CLI]", payload["cliScopes"])
	}
}

func TestHandlerHandleOAuthConfig_ExposesUsePopupLogin(t *testing.T) {
	h := NewHandler(&Config{
		OAuth: &OAuth{
			Mode:          "bff",
			UsePopupLogin: true,
			Client: &OAuthClient{
				ConfigURL:      testOAuthConfigURL,
				ClientID:       "client-id",
				WebUIScopes:    []string{"XXX_WEBUI"},
				MobileUIScopes: []string{"XXX_MOBILEUI"},
				CLIScopes:      []string{"XXX_CLI"},
			},
		},
	}, NewManager(0, nil))
	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/oauth/config", nil)
	rec := httptest.NewRecorder()

	h.handleOAuthConfig().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if got, ok := payload["usePopupLogin"].(bool); !ok || !got {
		t.Fatalf("usePopupLogin = %#v, want true", payload["usePopupLogin"])
	}
	if got, ok := payload["redirectSameTab"].(bool); !ok || got {
		t.Fatalf("redirectSameTab = %#v, want false", payload["redirectSameTab"])
	}
	if got := payload["clientId"]; got != "client-id" {
		t.Fatalf("clientId = %#v, want client-id", got)
	}
	if got := payload["configURL"]; got != testOAuthConfigURL {
		t.Fatalf("configURL = %#v, want encrypted config URL", got)
	}
	if got, ok := payload["scopes"].([]any); !ok || len(got) != 0 {
		t.Fatalf("scopes = %#v, want empty array", payload["scopes"])
	}
	if got, ok := payload["webUIScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_WEBUI" {
		t.Fatalf("webUIScopes = %#v, want [XXX_WEBUI]", payload["webUIScopes"])
	}
	if got, ok := payload["mobileUIScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_MOBILEUI" {
		t.Fatalf("mobileUIScopes = %#v, want [XXX_MOBILEUI]", payload["mobileUIScopes"])
	}
	if got, ok := payload["cliScopes"].([]any); !ok || len(got) != 1 || got[0] != "XXX_CLI" {
		t.Fatalf("cliScopes = %#v, want [XXX_CLI]", payload["cliScopes"])
	}
}
