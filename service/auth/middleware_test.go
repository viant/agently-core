package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtect_BFFOAuthRejectsBareLocalSessionCookie(t *testing.T) {
	cfg := &Config{
		Enabled:         true,
		CookieName:      "agently_session",
		DefaultUsername: "devuser",
		IpHashKey:       "dev-hmac-salt",
		OAuth: &OAuth{
			Mode: "bff",
		},
	}
	sessions := NewManager(0, nil)
	sessions.Put(nil, &Session{
		ID:       "sess-1",
		Username: "devuser",
	})

	handler := Protect(cfg, sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"userId": EffectiveUserID(r.Context()),
		})
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-1"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestProtect_BFFOAuthAcceptsTokenBackedSessionCookie(t *testing.T) {
	cfg := &Config{
		Enabled:    true,
		CookieName: "agently_session",
		IpHashKey:  "dev-hmac-salt",
		OAuth: &OAuth{
			Mode: "bff",
		},
	}
	sessions := NewManager(0, nil)
	sessions.Put(nil, &Session{
		ID:       "sess-1",
		Username: "devuser",
		Subject:  "user-123",
		Tokens:   newTokenBundle("access-token", "id-token", ""),
	})

	handler := Protect(cfg, sessions)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"userId": EffectiveUserID(r.Context()),
		})
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/conversations", nil)
	req.AddCookie(&http.Cookie{Name: "agently_session", Value: "sess-1"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "user-123")
}
