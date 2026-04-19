package auth

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// recordingTokenStore embeds the minimal TokenStore behaviour we need for the
// invalidate-flow tests: it records Delete calls so assertions can verify the
// stale persistent row was removed on permanent refresh failure.
type recordingTokenStore struct {
	testTokenStore
	deletes atomic.Int32
	lastUsr string
	lastPrv string
}

func (s *recordingTokenStore) Delete(_ context.Context, username, provider string) error {
	s.deletes.Add(1)
	s.lastUsr = username
	s.lastPrv = provider
	return nil
}

func TestIsPermanentRefreshError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("network issue"), false},
		{"invalid_grant code", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"invalid_token code (upper)", &oauth2.RetrieveError{ErrorCode: "INVALID_TOKEN"}, true},
		{"unauthorized_client", &oauth2.RetrieveError{ErrorCode: "unauthorized_client"}, true},
		{"unsupported_grant_type", &oauth2.RetrieveError{ErrorCode: "unsupported_grant_type"}, true},
		{"400 with no code", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}}, true},
		{"401 with no code", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusUnauthorized}}, true},
		{"500 transient", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
		{"timeout transient", &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusGatewayTimeout}}, false},
		{"server_error code", &oauth2.RetrieveError{ErrorCode: "server_error", Response: &http.Response{StatusCode: http.StatusInternalServerError}}, false},
	}
	for _, tc := range cases {
		if got := isPermanentRefreshError(tc.err); got != tc.want {
			t.Errorf("%s: isPermanentRefreshError = %v, want %v (err=%v)", tc.name, got, tc.want, tc.err)
		}
	}
}

// TestInvalidateSessionTokens_ClearsMemoryAndStore verifies that permanent
// refresh failure handling (a) wipes session tokens in memory so the next
// request doesn't retry the dead credential, and (b) deletes the persistent
// token row so a restart can't hydrate the dead tokens back into place.
func TestInvalidateSessionTokens_ClearsMemoryAndStore(t *testing.T) {
	store := &recordingTokenStore{}
	sessions := NewManager(0, nil)
	r := &Runtime{
		cfg:      &Config{},
		sessions: sessions,
		ext: &authExtension{
			cfg:        &Config{OAuth: &OAuth{Name: "oauth"}},
			sessions:   sessions,
			tokenStore: store,
		},
	}
	sess := &Session{
		ID:       "sess-x",
		Subject:  "user-sub",
		Provider: "oauth",
		Tokens: &scyauth.Token{
			Token: oauth2.Token{
				AccessToken:  "expired",
				RefreshToken: "dead-refresh",
				Expiry:       time.Now().Add(-time.Minute),
			},
			IDToken: "stale-id",
		},
	}
	sessions.Put(context.Background(), sess)

	r.invalidateSessionTokens(context.Background(), sess, "user-42", "oauth")

	if sess.Tokens != nil {
		t.Fatalf("session tokens should be nil after invalidation, got %#v", sess.Tokens)
	}
	if got := store.deletes.Load(); got != 1 {
		t.Fatalf("expected exactly one token-store Delete, got %d", got)
	}
	if store.lastUsr != "user-42" || store.lastPrv != "oauth" {
		t.Fatalf("Delete called with (%q, %q), want (user-42, oauth)", store.lastUsr, store.lastPrv)
	}
}

// TestInvalidateSessionTokens_NilStoreIsSafe guards against a nil token store
// — some deployments configure sessions without the persistent oauth token
// store and the invalidate path must still wipe memory without panicking.
func TestInvalidateSessionTokens_NilStoreIsSafe(t *testing.T) {
	sessions := NewManager(0, nil)
	r := &Runtime{cfg: &Config{}, sessions: sessions, ext: &authExtension{cfg: &Config{}, sessions: sessions}}
	sess := &Session{ID: "s1", Tokens: &scyauth.Token{Token: oauth2.Token{RefreshToken: "x"}}}
	r.invalidateSessionTokens(context.Background(), sess, "u", "p")
	if sess.Tokens != nil {
		t.Fatalf("expected tokens cleared, got %#v", sess.Tokens)
	}
}
