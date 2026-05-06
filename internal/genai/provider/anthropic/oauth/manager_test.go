package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type staticClientLoader struct {
	cfg *OAuthClientConfig
}

func (s *staticClientLoader) Load(context.Context) (*OAuthClientConfig, error) {
	return s.cfg, nil
}

type memoryStore struct {
	state *TokenState
}

func (m *memoryStore) Load(context.Context) (*TokenState, error) {
	return m.state, nil
}

func (m *memoryStore) Save(_ context.Context, state *TokenState) error {
	m.state = state
	return nil
}

func TestManagerAccessTokenRefreshesState(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/oauth/token", r.URL.Path)
		_, _ = w.Write([]byte(`{"access_token":"fresh-token","refresh_token":"fresh-refresh","expires_in":3600,"scope":"user:profile user:inference"}`))
	}))
	defer srv.Close()

	store := &memoryStore{state: &TokenState{
		AccessToken:  "expired",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}}
	manager, err := NewManager(&Options{TokenURL: srv.URL + "/v1/oauth/token"}, &staticClientLoader{
		cfg: &OAuthClientConfig{ClientID: "client-id"},
	}, store, srv.Client())
	require.NoError(t, err)

	token, err := manager.AccessToken(context.Background())
	require.NoError(t, err)
	require.Equal(t, "fresh-token", token)
	require.Equal(t, 1, calls)
	require.Equal(t, "fresh-refresh", store.state.RefreshToken)
	require.True(t, store.state.ExpiresAt.After(time.Now()))
}
