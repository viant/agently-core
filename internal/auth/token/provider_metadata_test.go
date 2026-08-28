package token

import (
	"context"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// TestManagerRefreshPreservesMetadata verifies a manager-driven refresh never
// drops stored delegated metadata (issuer, resource, scopes, token type,
// subject, provider references) through the Put persistence path.
func TestManagerRefreshPreservesMetadata(t *testing.T) {
	stored := &OAuthToken{
		Username:     "user-1",
		Provider:     "corp-idp",
		AccessToken:  "stale-access",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(30 * time.Second),
		Issuer:       "https://idp.example.com",
		Resource:     "https://mcp.example.com/mcp",
		Scopes:       []string{"plan:create", "plan:read"},
		TokenType:    "accessToken",
		Subject:      "external-subject",
		ProviderRef:  "corp-idp",
		ClientRef:    "web",
	}
	var persisted *OAuthToken
	store := &mockTokenStore{
		getFunc: func(ctx context.Context, username, provider string) (*OAuthToken, error) {
			clone := *stored
			return &clone, nil
		},
		putFunc: func(ctx context.Context, token *OAuthToken) error {
			clone := *token
			persisted = &clone
			return nil
		},
	}
	broker := &recordingBroker{result: &scyauth.Token{Token: oauth2.Token{
		AccessToken:  "fresh-access",
		RefreshToken: "refresh-2",
		Expiry:       time.Now().Add(time.Hour),
	}}}
	router := &RoutingBroker{Workspace: broker, IsWorkspaceAlias: workspaceAliasMatcher("corp-idp")}
	mgr := NewManager(WithTokenStore(store), WithBroker(router), WithInstanceID(""))

	_, err := mgr.EnsureTokens(context.Background(), Key{Subject: "user-1", Provider: "corp-idp"})
	if err != nil {
		t.Fatalf("EnsureTokens: %v", err)
	}
	if broker.refreshN != 1 {
		t.Fatalf("broker refresh count = %d, want 1", broker.refreshN)
	}
	if persisted == nil {
		t.Fatalf("expected refreshed token persisted")
	}
	if persisted.AccessToken != "fresh-access" || persisted.RefreshToken != "refresh-2" {
		t.Fatalf("unexpected persisted token values: %+v", persisted)
	}
	if persisted.Issuer != stored.Issuer || persisted.Resource != stored.Resource ||
		persisted.TokenType != stored.TokenType || persisted.Subject != stored.Subject ||
		persisted.ProviderRef != stored.ProviderRef || persisted.ClientRef != stored.ClientRef {
		t.Fatalf("refresh dropped stored metadata: %+v", persisted)
	}
	if len(persisted.Scopes) != 2 || persisted.Scopes[0] != "plan:create" {
		t.Fatalf("refresh dropped stored scopes: %v", persisted.Scopes)
	}
}

// TestPersistableRefreshTokenRequiresExactProviderMatch proves the refresh
// persistence path never merges metadata from a row the legacy Get fallback
// served under a different provider — in particular a delegated (mcp:v1) row
// must never donate metadata to a workspace token.
func TestPersistableRefreshTokenRequiresExactProviderMatch(t *testing.T) {
	fallbackRow := &OAuthToken{
		Username:    "user-1",
		Provider:    "mcp:v1:0000000000000000000000000000000000000000000000000000000000000000",
		Issuer:      "https://mcp-idp.example.com",
		Resource:    "https://mcp.example.com/mcp",
		Subject:     "mcp-subject",
		ProviderRef: "adelphic-dev6",
	}
	store := &mockTokenStore{
		getFunc: func(ctx context.Context, username, provider string) (*OAuthToken, error) {
			clone := *fallbackRow
			return &clone, nil
		},
	}
	mgr := NewManager(WithTokenStore(store), WithInstanceID(""))
	fresh := &scyauth.Token{Token: oauth2.Token{AccessToken: "fresh", Expiry: time.Now().Add(time.Hour)}}

	next := mgr.persistableRefreshToken(context.Background(), Key{Subject: "user-1", Provider: "corp-idp"}, fresh)
	if next.Issuer != "" || next.Resource != "" || next.Subject != "" || next.ProviderRef != "" {
		t.Fatalf("metadata from a different-provider row must not be merged: %+v", next)
	}
	if next.IssuedAt.IsZero() {
		t.Fatalf("refresh persistence must stamp issued-at")
	}

	// The exact same provider row still donates its metadata.
	fallbackRow.Provider = "corp-idp"
	merged := mgr.persistableRefreshToken(context.Background(), Key{Subject: "user-1", Provider: "corp-idp"}, fresh)
	if merged.Issuer != fallbackRow.Issuer || merged.Subject != fallbackRow.Subject {
		t.Fatalf("exact-provider metadata must merge: %+v", merged)
	}
}

func TestMergeMetadataFromGuardsIDTokenExpiry(t *testing.T) {
	priorExpiry := time.Now().Add(time.Hour).Truncate(time.Second)
	prior := &OAuthToken{IDToken: "id-old", IDTokenExpiresAt: priorExpiry, IssuedAt: time.Now().Add(-time.Hour)}

	// Retained ID token: the recorded expiry carries over.
	retained := &OAuthToken{IDToken: "id-old"}
	retained.MergeMetadataFrom(prior)
	if !retained.IDTokenExpiresAt.Equal(priorExpiry) {
		t.Fatalf("retained id token must keep its recorded expiry")
	}

	// Dropped or rotated ID token: a stale expiry must never resurrect.
	dropped := &OAuthToken{}
	dropped.MergeMetadataFrom(prior)
	if !dropped.IDTokenExpiresAt.IsZero() {
		t.Fatalf("dropped id token must not inherit a stale expiry")
	}
	rotated := &OAuthToken{IDToken: "id-new"}
	rotated.MergeMetadataFrom(prior)
	if !rotated.IDTokenExpiresAt.IsZero() {
		t.Fatalf("rotated id token must not inherit the previous token's expiry")
	}
}

func TestMergeMetadataFromPrefersReceiver(t *testing.T) {
	next := &OAuthToken{Scopes: []string{"narrowed"}}
	next.MergeMetadataFrom(&OAuthToken{
		Issuer: "https://idp.example.com",
		Scopes: []string{"plan:create", "plan:read"},
	})
	if next.Issuer != "https://idp.example.com" {
		t.Fatalf("missing issuer must be filled from prior")
	}
	// An authoritative (narrowed) refreshed scope set must win over stale scopes.
	if len(next.Scopes) != 1 || next.Scopes[0] != "narrowed" {
		t.Fatalf("populated scopes must win over prior: %v", next.Scopes)
	}
}
