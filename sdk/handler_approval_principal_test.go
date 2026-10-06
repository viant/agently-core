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

type approvalPrincipalClient struct {
	Client
	principal string
	userID    string
}

func (c *approvalPrincipalClient) DecideToolApproval(ctx context.Context, in *DecideToolApprovalInput) (*DecideToolApprovalOutput, error) {
	c.principal = iauth.EffectiveUserID(ctx)
	c.userID = in.UserID
	return &DecideToolApprovalOutput{Status: "ok"}, nil
}
func TestApprovalHTTPUsesStableAnonymousPrincipal(t *testing.T) {
	c := &approvalPrincipalClient{}
	req := httptest.NewRequest("POST", "/v1/tool-approvals/id/decision", strings.NewReader(`{"action":"approve"}`))
	req.SetPathValue("id", "id")
	req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:fixture"})
	out := httptest.NewRecorder()
	handleDecideToolApproval(c)(out, req)
	require.Equal(t, 200, out.Code, out.Body.String())
	require.Equal(t, "anonymous:fixture", c.principal)
	require.Equal(t, c.principal, c.userID)
}
func TestApprovalHTTPRejectsMissingAuthenticatedPrincipal(t *testing.T) {
	c := &approvalPrincipalClient{}
	req := httptest.NewRequest("POST", "/v1/tool-approvals/id/decision", strings.NewReader(`{"action":"approve","userId":"forged"}`))
	req.SetPathValue("id", "id")
	req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:fixture"})
	out := httptest.NewRecorder()
	handleDecideToolApproval(c, &svcauth.Config{Enabled: true})(out, req)
	require.Equal(t, 401, out.Code)
	require.Empty(t, c.principal)
}
func TestApprovalHTTPLocalExplicitIdentityRemainsSupported(t *testing.T) {
	c := &approvalPrincipalClient{}
	req := httptest.NewRequest("POST", "/v1/tool-approvals/id/decision", strings.NewReader(`{"action":"approve","userId":"local-cli"}`))
	req.SetPathValue("id", "id")
	out := httptest.NewRecorder()
	handleDecideToolApproval(c)(out, req)
	require.Equal(t, 200, out.Code)
	require.Equal(t, "local-cli", c.principal)
}
