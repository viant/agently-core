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

func (c *conversationIdentityClient) GetConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	c.owner = iauth.EffectiveUserID(ctx)
	return &conversation.Conversation{Id: id, CreatedByUserId: &c.owner}, nil
}
func (c *conversationIdentityClient) aguiThreadReference(ctx context.Context, _ string) (*string, error) {
	value := "bound:" + iauth.EffectiveUserID(ctx)
	return &value, nil
}
func TestGetConversationUsesSamePrincipalForHeaderAndThreadBinding(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		t.Run(map[bool]string{false: "anonymous", true: "authenticated"}[authenticated], func(t *testing.T) {
			client := &conversationIdentityClient{}
			req := httptest.NewRequest("GET", "/v1/conversations/thread?includeTranscript=false", nil)
			req.SetPathValue("id", "thread")
			req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:header-fixture"})
			expected := "anonymous:header-fixture"
			if authenticated {
				expected = "authenticated-owner"
				req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: expected}))
			}
			response := httptest.NewRecorder()
			handleGetConversation(client, &svcauth.Config{Enabled: authenticated})(response, req)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, expected, client.owner)
			require.Contains(t, response.Body.String(), `"aguiThreadId":"bound:`+expected+`"`)
		})
	}
}
func TestGetConversationAuthenticatedModeRejectsAnonymousCookie(t *testing.T) {
	client := &conversationIdentityClient{}
	req := httptest.NewRequest("GET", "/v1/conversations/thread", nil)
	req.SetPathValue("id", "thread")
	req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:header-fixture"})
	response := httptest.NewRecorder()
	handleGetConversation(client, &svcauth.Config{Enabled: true})(response, req)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Empty(t, client.owner)
}
