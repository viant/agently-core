package auth

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
)

// TestChooseTokenRowNeverFallsBackToDelegatedRow proves fix semantics for the
// legacy provider fallback: exact requested matches (including exact delegated
// storage keys) are served, but fallback candidates always exclude delegated
// (mcp:v1) rows.
func TestChooseTokenRowNeverFallsBackToDelegatedRow(t *testing.T) {
	delegatedKey := DelegatedProviderStorageKey("ns", "adelphic-dev6")
	delegatedRow := tokenRow{userID: "uuid-1", provider: delegatedKey, enc: "delegated-enc"}
	workspaceRow := tokenRow{userID: "uuid-1", provider: "corp-idp", enc: "workspace-enc"}

	// Requested provider missing: the workspace row is served via fallback and
	// the delegated row is skipped even though it sorts first.
	selected, viaFallback := chooseTokenRow([]tokenRow{delegatedRow, workspaceRow}, "jwt")
	if selected == nil || selected.provider != "corp-idp" || !viaFallback {
		t.Fatalf("fallback must serve the first non-delegated row, got %+v (fallback=%v)", selected, viaFallback)
	}

	// Only a delegated row exists: a mismatched request gets a miss, never the
	// delegated credential.
	if selected, _ := chooseTokenRow([]tokenRow{delegatedRow}, "corp-idp"); selected != nil {
		t.Fatalf("a delegated row must never satisfy a workspace fallback, got %+v", selected)
	}

	// Empty requested provider (legacy alias path): same exclusion applies.
	if selected, _ := chooseTokenRow([]tokenRow{delegatedRow}, ""); selected != nil {
		t.Fatalf("empty-provider fallback must never serve a delegated row, got %+v", selected)
	}
	selected, viaFallback = chooseTokenRow([]tokenRow{delegatedRow, workspaceRow}, "")
	if selected == nil || selected.provider != "corp-idp" || !viaFallback {
		t.Fatalf("empty-provider fallback must serve the workspace row, got %+v", selected)
	}

	// An exact delegated request still matches exactly (not via fallback).
	selected, viaFallback = chooseTokenRow([]tokenRow{workspaceRow, delegatedRow}, delegatedKey)
	if selected == nil || selected.provider != delegatedKey || viaFallback {
		t.Fatalf("exact delegated key must match exactly, got %+v (fallback=%v)", selected, viaFallback)
	}

	// An exact workspace request wins over any fallback candidate.
	selected, viaFallback = chooseTokenRow([]tokenRow{delegatedRow, workspaceRow}, "corp-idp")
	if selected == nil || selected.provider != "corp-idp" || viaFallback {
		t.Fatalf("exact workspace match must win, got %+v (fallback=%v)", selected, viaFallback)
	}
}

// TestTokenStoreDelegatedSaltSelection proves delegated (mcp:v1) rows encrypt
// under the explicit tokenEncryptionKey-derived salt while workspace rows keep
// the legacy salt, and that the two are not interchangeable.
func TestTokenStoreDelegatedSaltSelection(t *testing.T) {
	delegatedKey := DelegatedProviderStorageKey("ns", "adelphic-dev6")
	store := NewTokenStoreDAO(nil, "workspace-salt", WithDelegatedSalt("explicit-token-encryption-key"))

	delegated := &OAuthToken{Provider: delegatedKey, AccessToken: "delegated-access", ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second)}
	enc, err := store.encrypt(context.Background(), delegated)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	decoded, err := store.decrypt(context.Background(), enc, delegatedKey)
	if err != nil || decoded.AccessToken != "delegated-access" {
		t.Fatalf("delegated round trip failed: %v %+v", err, decoded)
	}
	// The same payload does not decrypt under the workspace salt.
	if decoded, err := store.decrypt(context.Background(), enc, "corp-idp"); err == nil && decoded != nil && decoded.AccessToken == "delegated-access" {
		t.Fatalf("delegated payload must not decrypt under the workspace salt")
	}

	// Workspace rows keep using the base salt: a legacy store without the
	// delegated option reads them unchanged.
	workspace := &OAuthToken{Provider: "corp-idp", AccessToken: "workspace-access"}
	encWorkspace, err := store.encrypt(context.Background(), workspace)
	if err != nil {
		t.Fatalf("encrypt workspace: %v", err)
	}
	legacyStore := NewTokenStoreDAO(nil, "workspace-salt")
	decodedWorkspace, err := legacyStore.decrypt(context.Background(), encWorkspace, "corp-idp")
	if err != nil || decodedWorkspace.AccessToken != "workspace-access" {
		t.Fatalf("workspace rows must stay readable by a legacy single-salt store: %v %+v", err, decodedWorkspace)
	}

	// Without an explicit delegated salt the base salt applies everywhere
	// (backward-compatible fallback).
	fallbackStore := NewTokenStoreDAO(nil, "workspace-salt")
	encFallback, err := fallbackStore.encrypt(context.Background(), delegated)
	if err != nil {
		t.Fatalf("encrypt fallback: %v", err)
	}
	if decoded, err := legacyStore.decrypt(context.Background(), encFallback, delegatedKey); err != nil || decoded.AccessToken != "delegated-access" {
		t.Fatalf("configURL-salt fallback must keep delegated rows readable: %v", err)
	}
}

func TestCanonicalTokenStoreSalt_LocalSCYEquivalence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	relative := "idp_viant.enc|blowfish://default"
	absolute := filepath.Join(home, ".secret", "idp_viant.enc") + "|blowfish://default"
	if got := canonicalTokenStoreSalt(relative); got != absolute {
		t.Fatalf("canonical relative salt = %q, want %q", got, absolute)
	}
	if got := canonicalTokenStoreSalt(absolute); got != absolute {
		t.Fatalf("canonical absolute salt = %q, want %q", got, absolute)
	}
	remote := "gs://secret-bucket/idp_viant.enc|blowfish://rotated/key"
	if got := canonicalTokenStoreSalt(remote); got != remote {
		t.Fatalf("non-file URL changed: got %q want %q", got, remote)
	}
}

func TestTokenStorePreviousSalts_AreAllowlistedAndExcludeDelegated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	relative := "idp_viant.enc|blowfish://default"
	absolute := filepath.Join(home, ".secret", "idp_viant.enc") + "|blowfish://default"

	legacy := &TokenStoreDAO{salt: relative}
	workspace := &OAuthToken{Provider: "oauth", AccessToken: "workspace-access"}
	legacyEnc, err := legacy.encrypt(context.Background(), workspace)
	if err != nil {
		t.Fatalf("legacy encrypt: %v", err)
	}
	current := NewTokenStoreDAO(nil, absolute, WithPreviousSalts(relative))
	decoded, usedPrevious, err := current.decryptWithPrevious(context.Background(), legacyEnc, "oauth")
	if err != nil || decoded.AccessToken != "workspace-access" || !usedPrevious {
		t.Fatalf("previous-salt read = token %+v previous=%v err=%v", decoded, usedPrevious, err)
	}
	if _, _, err := NewTokenStoreDAO(nil, absolute).decryptWithPrevious(context.Background(), legacyEnc, "oauth"); err == nil {
		t.Fatalf("unrelated/unconfigured legacy salt must fail closed")
	}
	// A process still configured with the relative SCY locator writes with the
	// same canonical active key as a process configured with the absolute path,
	// while automatically retaining its own pre-canonicalization text for reads.
	relativeConfigured := NewTokenStoreDAO(nil, relative)
	if decoded, previous, err := relativeConfigured.decryptWithPrevious(context.Background(), legacyEnc, "oauth"); err != nil || decoded.AccessToken != "workspace-access" || !previous {
		t.Fatalf("relative configured store did not read its legacy raw salt: token=%+v previous=%v err=%v", decoded, previous, err)
	}
	canonicalEnc, err := relativeConfigured.encrypt(context.Background(), workspace)
	if err != nil {
		t.Fatalf("canonical encrypt from relative config: %v", err)
	}
	if decoded, err := NewTokenStoreDAO(nil, absolute).decrypt(context.Background(), canonicalEnc, "oauth"); err != nil || decoded.AccessToken != "workspace-access" {
		t.Fatalf("relative/absolute configured processes do not share the canonical write key: %v %+v", err, decoded)
	}

	delegatedProvider := DelegatedProviderStorageKey("ns", "provider")
	delegatedLegacy := &TokenStoreDAO{salt: relative}
	delegatedEnc, err := delegatedLegacy.encrypt(context.Background(), &OAuthToken{
		Provider: delegatedProvider, AccessToken: "delegated-access",
	})
	if err != nil {
		t.Fatalf("delegated legacy encrypt: %v", err)
	}
	delegatedCurrent := NewTokenStoreDAO(nil, absolute,
		WithDelegatedSalt("delegated-current-key"), WithPreviousSalts(relative))
	if _, _, err := delegatedCurrent.decryptWithPrevious(context.Background(), delegatedEnc, delegatedProvider); err == nil {
		t.Fatalf("workspace previous salts must never decrypt delegated provider rows")
	}
}

func TestTokenStorePreviousSaltRead_MigratesWithCiphertextCAS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	relative := "idp_viant.enc|blowfish://default"
	absolute := filepath.Join(home, ".secret", "idp_viant.enc") + "|blowfish://default"
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory: %v", err)
	}
	users := NewDatlyUserService(dao)
	userID, err := users.UpsertWithProvider(ctx, "salt-user", "salt-user", "salt@example.test", "oauth", "salt-subject")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	store := NewTokenStoreDAO(dao, absolute, WithPreviousSalts(relative))
	tok := &OAuthToken{Username: userID, Provider: "oauth", AccessToken: "access", RefreshToken: "refresh"}
	if err := store.Put(ctx, tok); err != nil {
		t.Fatalf("Put: %v", err)
	}
	legacy := &TokenStoreDAO{salt: relative}
	legacyEnc, err := legacy.encrypt(ctx, tok)
	if err != nil {
		t.Fatalf("legacy encrypt: %v", err)
	}
	db, err := store.db()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE user_oauth_token SET enc_token = ? WHERE user_id = ? AND provider = ?`, legacyEnc, userID, "oauth"); err != nil {
		t.Fatalf("seed legacy ciphertext: %v", err)
	}
	var versionBefore int64
	if err := db.QueryRowContext(ctx, `SELECT version FROM user_oauth_token WHERE user_id = ? AND provider = ?`, userID, "oauth").Scan(&versionBefore); err != nil {
		t.Fatalf("version before: %v", err)
	}
	got, err := store.GetExact(ctx, userID, "oauth")
	if err != nil || got == nil || got.AccessToken != "access" {
		t.Fatalf("GetExact legacy row = %+v err=%v", got, err)
	}
	var migratedEnc string
	var versionAfter int64
	if err := db.QueryRowContext(ctx, `SELECT enc_token, version FROM user_oauth_token WHERE user_id = ? AND provider = ?`, userID, "oauth").Scan(&migratedEnc, &versionAfter); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if migratedEnc == legacyEnc || versionAfter != versionBefore+1 {
		t.Fatalf("migration ciphertext/version = changed:%v version:%d want %d", migratedEnc != legacyEnc, versionAfter, versionBefore+1)
	}
	activeOnly := NewTokenStoreDAO(nil, absolute)
	if decoded, err := activeOnly.decrypt(ctx, migratedEnc, "oauth"); err != nil || decoded.AccessToken != "access" {
		t.Fatalf("migrated ciphertext is not active-key readable: %v %+v", err, decoded)
	}

	// A stale migration attempt must not overwrite the ciphertext produced by
	// the first reader/refresh because the old ciphertext no longer matches.
	if err := store.migrateCiphertext(ctx, userID, "oauth", legacyEnc, got); err != nil {
		t.Fatalf("stale migration: %v", err)
	}
	var afterStale string
	if err := db.QueryRowContext(ctx, `SELECT enc_token FROM user_oauth_token WHERE user_id = ? AND provider = ?`, userID, "oauth").Scan(&afterStale); err != nil {
		t.Fatalf("read after stale migration: %v", err)
	}
	if afterStale != migratedEnc {
		t.Fatalf("stale migration overwrote concurrent ciphertext")
	}
}
