package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type cancelAwareUserService struct {
	userID string
}

func (c *cancelAwareUserService) GetByUsername(_ context.Context, username string) (*User, error) {
	return &User{ID: c.userID, Username: username}, nil
}

func (c *cancelAwareUserService) GetBySubjectAndProvider(_ context.Context, _, _ string) (*User, error) {
	return nil, nil
}

func (c *cancelAwareUserService) Upsert(_ context.Context, _ *User) error { return nil }

func (c *cancelAwareUserService) UpsertWithProvider(ctx context.Context, username, displayName, email, provider, subject string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.userID == "" {
		c.userID = "user-ctx-ok"
	}
	return c.userID, nil
}

func (c *cancelAwareUserService) UpdateHashIPByID(_ context.Context, _, _ string) error { return nil }

func (c *cancelAwareUserService) UpdatePreferences(_ context.Context, _ string, _ *PreferencesPatch) error {
	return nil
}

type cancelAwareTokenStore struct {
	putUser string
}

func (c *cancelAwareTokenStore) Get(_ context.Context, _, _ string) (*OAuthToken, error) {
	return nil, nil
}

func (c *cancelAwareTokenStore) Put(ctx context.Context, token *OAuthToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.putUser = token.Username
	return nil
}

func (c *cancelAwareTokenStore) Delete(_ context.Context, _, _ string) error { return nil }

func (c *cancelAwareTokenStore) TryAcquireRefreshLease(_ context.Context, _, _, _ string, _ time.Duration) (int64, bool, error) {
	return 0, false, nil
}

func (c *cancelAwareTokenStore) ReleaseRefreshLease(_ context.Context, _, _, _ string) error {
	return nil
}

func (c *cancelAwareTokenStore) CASPut(_ context.Context, _ *OAuthToken, _ int64, _ string) (bool, error) {
	return false, nil
}

func TestRuntimeHandleCreateSession_PersistsOAuthTokenWithDurableContext(t *testing.T) {
	store := &cancelAwareTokenStore{}
	users := &cancelAwareUserService{userID: "user-42"}
	ext := &authExtension{
		cfg: &Config{
			CookieName: "agently_session",
			OAuth:      &OAuth{Name: "oauth", Mode: "bff"},
		},
		sessions:   NewManager(time.Hour, nil),
		tokenStore: store,
		users:      users,
	}

	exp := time.Now().Add(90 * time.Minute).UTC().Truncate(time.Second)
	claims := map[string]any{
		"sub":                "user-123",
		"email":              "dev@example.com",
		"preferred_username": "devuser",
		"exp":                exp.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	idToken := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"

	parentCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/api/auth/session", strings.NewReader(
		`{"username":"devuser","idToken":"`+idToken+`","accessToken":"token-access"}`,
	)).WithContext(parentCtx)
	rec := httptest.NewRecorder()

	ext.handleCreateSession().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if store.putUser != "user-42" {
		t.Fatalf("persisted token user = %q, want %q", store.putUser, "user-42")
	}
}
