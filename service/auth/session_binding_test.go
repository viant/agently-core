package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	iauth "github.com/viant/agently-core/internal/auth"
	scyauth "github.com/viant/scy/auth"
)

func TestMeSessionBindingFencesSameSubjectCredentialRotationWithoutSecrets(t *testing.T) {
	session := &Session{ID: "private-cookie-value", Provider: "fixture", Subject: "same-user", Tokens: &scyauth.Token{IDToken: "private-id-token-A"}}
	first := meSessionBinding(context.Background(), session.Provider, session.Subject, session)
	if len(first) != 64 || strings.Contains(first, "private") {
		t.Fatalf("invalid opaque binding %q", first)
	}
	if next := meSessionBinding(context.Background(), session.Provider, session.Subject, session); next != first {
		t.Fatal("unchanged credential changed binding")
	}
	session.Tokens.IDToken = "private-id-token-B"
	if next := meSessionBinding(context.Background(), session.Provider, session.Subject, session); next == first {
		t.Fatal("same-subject credential/account change reused old binding")
	}
	if next := meSessionBinding(context.Background(), session.Provider, "different-user", session); next == first {
		t.Fatal("subject switch reused old binding")
	}
}

func TestAuthMePublishesOpaqueBindingForServerSessionAndNoRawCredential(t *testing.T) {
	sessions := NewManager(time.Hour, nil)
	ext := &authExtension{cfg: &Config{CookieName: "agently_session", Local: &Local{Enabled: true}}, sessions: sessions}
	session := &Session{ID: "private-session-cookie", Provider: "local", Subject: "fixture-user", Username: "fixture-user", Tokens: &scyauth.Token{IDToken: "private-fixture-ID-token"}}
	sessions.Put(context.Background(), session)
	request := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: "agently_session", Value: session.ID})
	response := httptest.NewRecorder()
	ext.handleMe().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sessionBinding"] != meSessionBinding(context.Background(), session.Provider, session.Subject, session) {
		t.Fatalf("binding response=%v", payload["sessionBinding"])
	}
	if strings.Contains(response.Body.String(), session.ID) || strings.Contains(response.Body.String(), session.Tokens.IDToken) {
		t.Fatal("auth/me leaked a credential or session cookie")
	}
}

func TestMeBindingUsesCurrentActorAndCanonicalOwner(t *testing.T) {
	sessions := NewManager(time.Hour, nil)
	ext := &authExtension{cfg: &Config{CookieName: "session"}, sessions: sessions}
	stale := &Session{ID: "old-cookie", Subject: "cookie-user", Provider: "provider", UserID: "canonical-old"}
	sessions.Put(context.Background(), stale)
	ctx := withRuntimeAuthUser(context.Background(), &runtimeAuthUser{Subject: "bearer-user", CanonicalUserID: "canonical-current", Provider: "jwt", Tokens: &scyauth.Token{IDToken: "current-credential"}})
	req := httptest.NewRequest(http.MethodGet, "/v1/api/auth/me", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: "session", Value: stale.ID})
	rec := httptest.NewRecorder()
	ext.handleMe().ServeHTTP(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["subject"] != "bearer-user" {
		t.Fatalf("stale cookie replaced authenticated actor: %v", payload["subject"])
	}
	want := meSessionBinding(ctx, "jwt", "bearer-user", nil)
	if payload["sessionBinding"] != want {
		t.Fatalf("binding did not use current actor")
	}
	first := meSessionBinding(iauth.WithCanonicalUserID(context.Background(), "canonical-A"), "jwt", "same-user", nil)
	second := meSessionBinding(iauth.WithCanonicalUserID(context.Background(), "canonical-B"), "jwt", "same-user", nil)
	if first == second {
		t.Fatal("canonical actor change reused old binding")
	}
}
