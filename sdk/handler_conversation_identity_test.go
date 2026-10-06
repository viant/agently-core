package sdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	iauth "github.com/viant/agently-core/internal/auth"
	svcauth "github.com/viant/agently-core/service/auth"
)

type conversationIdentityClient struct {
	Client
	owner string
}

func (c *conversationIdentityClient) CreateConversation(ctx context.Context, _ *CreateConversationInput) (*conversation.Conversation, error) {
	c.owner = iauth.EffectiveUserID(ctx)
	return &conversation.Conversation{Id: "thread", CreatedByUserId: &c.owner}, nil
}

func TestCreateConversationUsesProtocolPrincipal(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(map[bool]string{false: "anonymous", true: "authenticated"}[authenticated], func(t *testing.T) {
			client := &conversationIdentityClient{}
			req := httptest.NewRequest("POST", "/v1/conversations", strings.NewReader(`{"agentId":"simple"}`))
			req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:other"})
			if authenticated {
				req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "authenticated-owner"}))
			}
			response := httptest.NewRecorder()
			cfg := &svcauth.Config{Enabled: authenticated}
			handleCreateConversation(client, cfg)(response, req)
			require.Equal(t, 200, response.Code, response.Body.String())
			require.Equal(t, resolveQueryUserID(httptest.NewRecorder(), req, "", cfg), client.owner)
			if authenticated {
				require.Equal(t, "authenticated-owner", client.owner)
			}
		})
	}
}
func TestCreateConversationEstablishesAnonymousCookie(t *testing.T) {
	client := &conversationIdentityClient{}
	response := httptest.NewRecorder()
	handleCreateConversation(client, nil)(response, httptest.NewRequest("POST", "/v1/conversations", strings.NewReader(`{}`)))
	require.Equal(t, 200, response.Code)
	require.NotEmpty(t, client.owner)
	req := httptest.NewRequest("POST", "/v1/ag-ui/run", nil)
	for _, cookie := range response.Result().Cookies() {
		req.AddCookie(cookie)
	}
	require.Equal(t, client.owner, resolveQueryUserID(httptest.NewRecorder(), req, "", nil))
}
func TestCreateConversationRequiresAuthenticatedPrincipal(t *testing.T) {
	client := &conversationIdentityClient{}
	response := httptest.NewRecorder()
	handleCreateConversation(client, &svcauth.Config{Enabled: true})(response, httptest.NewRequest("POST", "/v1/conversations", strings.NewReader(`{}`)))
	require.Equal(t, 401, response.Code)
	require.Empty(t, client.owner)
	require.Empty(t, response.Result().Cookies())
}
