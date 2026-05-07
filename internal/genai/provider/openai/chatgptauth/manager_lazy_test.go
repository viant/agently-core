package chatgptauth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lazyStore struct {
	state *TokenState
}

func (s *lazyStore) Load(context.Context) (*TokenState, error) {
	if s.state == nil {
		return nil, &TokenStateNotFoundError{TokensURL: "memory"}
	}
	return s.state, nil
}

func (s *lazyStore) Save(_ context.Context, state *TokenState) error {
	s.state = state
	return nil
}

type lazyClientLoader struct{}

func (lazyClientLoader) Load(context.Context) (*OAuthClientConfig, error) {
	return &OAuthClientConfig{ClientID: "client"}, nil
}

func TestManagerAccessToken_SubscriptionAuth(t *testing.T) {
	store := &lazyStore{}
	mgr, err := NewManager(&Options{SubscriptionAuth: true}, lazyClientLoader{}, store, nil)
	require.NoError(t, err)

	prev := runLazyBrowserAuth
	defer func() { runLazyBrowserAuth = prev }()
	runLazyBrowserAuth = func(
		ctx context.Context,
		buildAuthorizeURL func(ctx context.Context, redirectURI, state, codeVerifier string) (string, error),
		exchangeCode func(ctx context.Context, redirectURI, codeVerifier, code string) error,
	) error {
		store.state = &TokenState{
			AccessToken:  "lazy-access",
			RefreshToken: "lazy-refresh",
			LastRefresh:  time.Now().UTC(),
		}
		return nil
	}

	token, err := mgr.AccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "lazy-access", token)
}
