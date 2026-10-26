package auth

import (
	"context"
	"testing"
	"time"
)

func TestTokenStoreCASClearPreservesNewerVersionAndForeignLease(t *testing.T) {
	ctx := context.Background()
	_, dbPath := newMCPLinkTestDBWithPath(t)
	native := newMCPLinkTestNative(t, dbPath)
	users := NewDatlyUserService(native)
	user, err := users.UpsertWithProvider(ctx, "conditional", "conditional", "conditional@example.test", "oauth", "conditional-subject")
	if err != nil {
		t.Fatal(err)
	}
	store := NewTokenStoreDAO(native, "conditional-clear-test")
	if err = store.Put(ctx, &OAuthToken{Username: user, Provider: "oauth", AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	version, claimed, err := store.TryAcquireRefreshLease(ctx, user, "oauth", "attempt-owner", time.Minute)
	if err != nil || !claimed {
		t.Fatal("claim unavailable", err)
	}
	for _, attempt := range []struct {
		version int64
		owner   string
	}{{version + 1, "attempt-owner"}, {version, "foreign-owner"}} {
		cleared, e := store.CASClear(ctx, user, "oauth", attempt.version, attempt.owner)
		if e != nil || cleared {
			t.Fatal("stale mutation accepted", e)
		}
		held, e := store.GetExact(ctx, user, "oauth")
		if e != nil || held == nil || held.RefreshToken != "old-refresh" {
			t.Fatal("stale clear changed credential")
		}
	}
	if err = store.Put(ctx, &OAuthToken{Username: user, Provider: "oauth", AccessToken: "new-login", RefreshToken: "new-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cleared, err := store.CASClear(ctx, user, "oauth", version, "attempt-owner")
	if err != nil || cleared {
		t.Fatal("old refresh erased new login", err)
	}
	held, err := store.GetExact(ctx, user, "oauth")
	if err != nil || held == nil || held.RefreshToken != "new-refresh" {
		t.Fatal("new login lost")
	}
	version, claimed, err = store.TryAcquireRefreshLease(ctx, user, "oauth", "new-attempt", time.Minute)
	if err != nil || !claimed {
		t.Fatal("new claim unavailable", err)
	}
	swapped, casErr := store.CASPut(ctx, &OAuthToken{Username: user, Provider: "oauth", AccessToken: "rotated-access", RefreshToken: "rotated-refresh", ExpiresAt: time.Now().Add(time.Hour)}, version, "new-attempt")
	if casErr != nil || !swapped {
		t.Fatal("matching refresh CAS unavailable", casErr)
	}
	db := authTokenTestDB(t, dbPath)
	var after int64
	var state string
	if err = db.QueryRowContext(ctx, "SELECT version,refresh_status FROM user_oauth_token WHERE user_id=? AND provider=?", user, "oauth").Scan(&after, &state); err != nil || after != version+1 || state != "idle" {
		t.Fatal("CASPut must increment exactly once and release lease", err)
	}
	version, claimed, err = store.TryAcquireRefreshLease(ctx, user, "oauth", "final-attempt", time.Minute)
	if err != nil || !claimed {
		t.Fatal("final claim unavailable", err)
	}
	cleared, err = store.CASClear(ctx, user, "oauth", version, "final-attempt")
	if err != nil || !cleared {
		t.Fatal("matching invalid credential not cleared", err)
	}
	held, err = store.GetExact(ctx, user, "oauth")
	if err != nil || held != nil {
		t.Fatal("matching clear retained credential")
	}
}
