package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerAccessToken_LazyBrowserAuth(t *testing.T) {
	store := &memoryStore{}
	manager, err := NewManager(&Options{LazyBrowserAuth: true}, &staticClientLoader{
		cfg: &OAuthClientConfig{ClientID: "client-id"},
	}, store, nil)
	require.NoError(t, err)

	prev := runLazyBrowserAuth
	defer func() { runLazyBrowserAuth = prev }()
	runLazyBrowserAuth = func(
		ctx context.Context,
		buildAuthorizeURL func(ctx context.Context, redirectURI, state, codeVerifier string) (string, error),
		exchangeCode func(ctx context.Context, redirectURI, codeVerifier, code string) error,
	) error {
		store.state = &TokenState{
			AccessToken:  "lazy-ant",
			RefreshToken: "lazy-refresh",
			ExpiresAt:    time.Now().Add(time.Hour),
			LastRefresh:  time.Now().UTC(),
		}
		return nil
	}

	token, err := manager.AccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "lazy-ant", token)
}
