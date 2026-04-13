package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthExtensionHandleOAuthConfig_ExposesRedirectSameTab(t *testing.T) {
	cfg := &Config{
		OAuth: &OAuth{
			Mode:            "bff",
			RedirectSameTab: true,
			Client: &OAuthClient{
				ConfigURL: "idp_viant.enc|blowfish://default",
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
	if got, ok := payload["redirectSameTab"].(bool); !ok || !got {
		t.Fatalf("redirectSameTab = %#v, want true", payload["redirectSameTab"])
	}
	if got := payload["configURL"]; got != "idp_viant.enc|blowfish://default" {
		t.Fatalf("configURL = %#v, want encrypted config URL", got)
	}
}

func TestHandlerHandleOAuthConfig_ExposesRedirectSameTab(t *testing.T) {
	h := NewHandler(&Config{
		OAuth: &OAuth{
			Mode:            "bff",
			RedirectSameTab: true,
			Client: &OAuthClient{
				ConfigURL: "idp_viant.enc|blowfish://default",
				ClientID:  "client-id",
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
	if got, ok := payload["redirectSameTab"].(bool); !ok || !got {
		t.Fatalf("redirectSameTab = %#v, want true", payload["redirectSameTab"])
	}
	if got := payload["clientId"]; got != "client-id" {
		t.Fatalf("clientId = %#v, want client-id", got)
	}
	if got := payload["configURL"]; got != "idp_viant.enc|blowfish://default" {
		t.Fatalf("configURL = %#v, want encrypted config URL", got)
	}
}
