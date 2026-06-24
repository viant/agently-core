package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandlerOOB_DerivesTokenExpiry(t *testing.T) {
	expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	sessions := NewManager(time.Hour, nil)
	h := NewHandler(&Config{
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name: "oauth",
		},
	}, sessions)
	body, err := json.Marshal(map[string]string{
		"username":    "awitas",
		"accessToken": "access-token",
		"idToken":     fakeJWTWithExp(t, expiry),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oob", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleOOB().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error = %v", err)
	}
	if resp.SessionID == "" {
		t.Fatalf("sessionId was empty")
	}
	sess := sessions.Get(context.Background(), resp.SessionID)
	if sess == nil || sess.Tokens == nil {
		t.Fatalf("session tokens were not stored")
	}
	if !sess.Tokens.Expiry.Equal(expiry) {
		t.Fatalf("token expiry = %v, want %v", sess.Tokens.Expiry, expiry)
	}
}

func TestHandlerOOB_DerivesSessionIdentityFromTokens(t *testing.T) {
	sessions := NewManager(time.Hour, nil)
	h := NewHandler(&Config{
		CookieName: "agently_session",
		OAuth: &OAuth{
			Name: "oauth",
		},
	}, sessions)
	idToken := fakeJWTWithClaims(t, map[string]any{
		"sub":                "subject-123",
		"email":              "person@example.test",
		"preferred_username": "person",
	})
	body, err := json.Marshal(map[string]string{
		"accessToken": "access-token",
		"idToken":     idToken,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/oob", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.handleOOB().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var resp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response error = %v", err)
	}
	sess := sessions.Get(context.Background(), resp.SessionID)
	if sess == nil {
		t.Fatalf("session was not stored")
	}
	if sess.Username != "person" {
		t.Fatalf("session username = %q, want %q", sess.Username, "person")
	}
	if sess.Subject != "subject-123" {
		t.Fatalf("session subject = %q, want %q", sess.Subject, "subject-123")
	}
	if sess.Email != "person@example.test" {
		t.Fatalf("session email = %q, want %q", sess.Email, "person@example.test")
	}
}

func fakeJWTWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
}
