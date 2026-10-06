package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	svcauth "github.com/viant/agently-core/service/auth"
)

type goalPrincipalClient struct {
	Client
	principal string
}

func (c *goalPrincipalClient) GetGoal(ctx context.Context, _ string) (*Goal, error) {
	c.principal = iauth.EffectiveUserID(ctx)
	return &Goal{}, nil
}
func (c *goalPrincipalClient) CreateGoal(ctx context.Context, _ *CreateGoalInput) (*Goal, error) {
	c.principal = iauth.EffectiveUserID(ctx)
	return &Goal{}, nil
}
func (c *goalPrincipalClient) UpdateGoal(ctx context.Context, _ *UpdateGoalInput) (*Goal, error) {
	c.principal = iauth.EffectiveUserID(ctx)
	return &Goal{}, nil
}
func (c *goalPrincipalClient) ClearGoal(ctx context.Context, _ string) error {
	c.principal = iauth.EffectiveUserID(ctx)
	return nil
}

func TestGoalHTTPPrincipalParity(t *testing.T) {
	for _, operation := range []struct {
		method  string
		handler func(Client, ...*svcauth.Config) http.HandlerFunc
	}{
		{"GET", handleGetGoal}, {"POST", handleCreateGoal}, {"PATCH", handleUpdateGoal}, {"DELETE", handleClearGoal},
	} {
		for _, mode := range []string{"local", "authenticated", "missing-auth"} {
			t.Run(operation.method+"/"+mode, func(t *testing.T) {
				client := &goalPrincipalClient{}
				req := httptest.NewRequest(operation.method, "/v1/conversations/thread/goal", strings.NewReader(`{"objective":"synthetic goal","status":"paused"}`))
				req.SetPathValue("id", "thread")
				req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:fixture"})
				if mode == "authenticated" {
					req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "owner"}))
				}
				out := httptest.NewRecorder()
				operation.handler(client, &svcauth.Config{Enabled: mode != "local"})(out, req)
				if mode == "missing-auth" {
					require.Equal(t, http.StatusUnauthorized, out.Code)
					require.Empty(t, client.principal)
					return
				}
				require.Less(t, out.Code, 300, out.Body.String())
				expected := "anonymous:fixture"
				if mode == "authenticated" {
					expected = "owner"
				}
				require.Equal(t, expected, client.principal)
			})
		}
	}
}
