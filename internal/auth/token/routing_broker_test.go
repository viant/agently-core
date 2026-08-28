package token

import (
	"context"
	"testing"
	"time"

	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

type recordingBroker struct {
	name     string
	refreshN int
	result   *scyauth.Token
	err      error
}

func (b *recordingBroker) Refresh(ctx context.Context, key Key, refreshToken string) (*scyauth.Token, error) {
	b.refreshN++
	if b.err != nil {
		return nil, b.err
	}
	if b.result != nil {
		return b.result, nil
	}
	return &scyauth.Token{Token: oauth2.Token{AccessToken: "from-" + b.name}}, nil
}

func (b *recordingBroker) Exchange(ctx context.Context, key Key, code string) (*scyauth.Token, error) {
	return nil, nil
}

type mapBrokerRegistry map[string]Broker

func (m mapBrokerRegistry) Broker(ctx context.Context, provider string) (Broker, bool) {
	b, ok := m[provider]
	return b, ok
}

func workspaceAliasMatcher(aliases ...string) WorkspaceAliasMatcher {
	set := map[string]bool{}
	for _, alias := range aliases {
		set[alias] = true
	}
	return func(provider string) bool { return set[provider] }
}

func TestRoutingBrokerWorkspaceAlias(t *testing.T) {
	workspace := &recordingBroker{name: "workspace"}
	router := &RoutingBroker{
		Workspace:        workspace,
		IsWorkspaceAlias: workspaceAliasMatcher("", "oauth", "jwt", "corp-idp"),
	}
	for _, alias := range []string{"oauth", "jwt", "corp-idp", ""} {
		if _, err := router.Refresh(context.Background(), Key{Subject: "u", Provider: alias}, "rt"); err != nil {
			t.Fatalf("alias %q: unexpected error %v", alias, err)
		}
	}
	if workspace.refreshN != 4 {
		t.Fatalf("workspace broker refresh count = %d, want 4", workspace.refreshN)
	}
}

func TestRoutingBrokerDelegatedProvider(t *testing.T) {
	workspace := &recordingBroker{name: "workspace"}
	delegated := &recordingBroker{name: "dev6"}
	router := &RoutingBroker{
		Workspace:        workspace,
		Registry:         mapBrokerRegistry{"mcp:v1:deadbeef": delegated},
		IsWorkspaceAlias: workspaceAliasMatcher("oauth"),
	}
	tok, err := router.Refresh(context.Background(), Key{Subject: "u", Provider: "mcp:v1:deadbeef"}, "rt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.AccessToken != "from-dev6" {
		t.Fatalf("routed to wrong broker: %q", tok.AccessToken)
	}
	if workspace.refreshN != 0 {
		t.Fatalf("delegated refresh must never touch the workspace broker")
	}
}

func TestRoutingBrokerUnknownProviderNeverHitsWorkspace(t *testing.T) {
	workspace := &recordingBroker{name: "workspace"}
	router := &RoutingBroker{
		Workspace:        workspace,
		Registry:         mapBrokerRegistry{},
		IsWorkspaceAlias: workspaceAliasMatcher("oauth"),
	}
	_, err := router.Refresh(context.Background(), Key{Subject: "u", Provider: "mcp:v1:unknown"}, "rt")
	if err == nil {
		t.Fatalf("expected error for unknown provider")
	}
	if !IsNoBrokerForProvider(err) {
		t.Fatalf("expected ErrNoBrokerForProvider, got %v", err)
	}
	// Missing broker configuration is not invalid_grant: the token manager
	// must apply a cooldown and never delete the row.
	if IsRefreshInvalidGrant(err) {
		t.Fatalf("missing broker routing must not classify as invalid_grant")
	}
	if workspace.refreshN != 0 {
		t.Fatalf("unknown provider must never be sent to the workspace broker")
	}
}

func TestManagerSkipsUnknownBrokerRowsWithoutDeletion(t *testing.T) {
	// A manager whose broker has no route for the key must preserve the
	// stored row: cooldown, no Delete, no Put.
	stored := &OAuthToken{
		Username:     "user-1",
		Provider:     "mcp:v1:unrouted",
		AccessToken:  "stale",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	store := &mockTokenStore{
		getFunc: func(ctx context.Context, username, provider string) (*OAuthToken, error) {
			if provider == stored.Provider {
				clone := *stored
				return &clone, nil
			}
			return nil, nil
		},
	}
	router := &RoutingBroker{
		Workspace:        &recordingBroker{name: "workspace"},
		Registry:         mapBrokerRegistry{},
		IsWorkspaceAlias: workspaceAliasMatcher("oauth"),
	}
	mgr := NewManager(WithTokenStore(store), WithBroker(router), WithInstanceID(""))
	ctx, err := mgr.EnsureTokens(context.Background(), Key{Subject: "user-1", Provider: "mcp:v1:unrouted"})
	if err != nil {
		t.Fatalf("EnsureTokens must not fail hard on missing routing: %v", err)
	}
	_ = ctx
	if store.deleteCalls != 0 {
		t.Fatalf("missing broker must never delete a token row (deletes=%d)", store.deleteCalls)
	}
	if store.putCalls != 0 {
		t.Fatalf("missing broker must never modify a token row (puts=%d)", store.putCalls)
	}
}
