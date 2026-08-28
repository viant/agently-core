package auth

import (
	"context"
	"testing"
	"time"
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
