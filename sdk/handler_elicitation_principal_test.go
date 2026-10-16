package sdk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	svcauth "github.com/viant/agently-core/service/auth"
)

type elicitationPrincipalClient struct {
	Client
	owner, principal string
	writes           int
}

func (c *elicitationPrincipalClient) ResolveElicitation(ctx context.Context, in *ResolveElicitationInput) error {
	c.principal = iauth.EffectiveUserID(ctx)
	if c.principal != c.owner {
		return fmt.Errorf("elicitation conversation is not owned by caller")
	}
	if in.ConversationID != "owned" || in.ElicitationID != "ask" {
		return fmt.Errorf("unexpected resolution target")
	}
	c.writes++
	return nil
}

func TestElicitationHTTPUsesApplicationPrincipalWithoutOwnerInference(t *testing.T) {
	for _, mode := range []string{"local-cookie", "authenticated-owner", "missing-auth", "wrong-owner", "local-new-cookie"} {
		t.Run(mode, func(t *testing.T) {
			client := &elicitationPrincipalClient{owner: "owner"}
			request := httptest.NewRequest(http.MethodPost, "/v1/elicitations/owned/ask/resolve", strings.NewReader(`{"action":"accept","payload":{"favoriteColor":"blue"},"userId":"owner","subject":"owner"}`))
			request.SetPathValue("conversationId", "owned")
			request.SetPathValue("elicitationId", "ask")
			request.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:fixture"})
			config := &svcauth.Config{Enabled: mode != "local-cookie" && mode != "local-new-cookie"}
			switch mode {
			case "local-cookie":
				client.owner = "anonymous:fixture"
			case "authenticated-owner":
				request = request.WithContext(iauth.WithUserInfo(request.Context(), &iauth.UserInfo{Subject: "owner"}))
			case "wrong-owner":
				request = request.WithContext(iauth.WithUserInfo(request.Context(), &iauth.UserInfo{Subject: "other"}))
			case "local-new-cookie":
				request.Header.Del("Cookie")
			}
			response := httptest.NewRecorder()
			handleResolveElicitation(client, config)(response, request)
			switch mode {
			case "local-cookie", "authenticated-owner":
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Equal(t, client.owner, client.principal)
				require.Equal(t, 1, client.writes)
			case "missing-auth":
				require.Equal(t, http.StatusUnauthorized, response.Code)
				require.Empty(t, client.principal)
				require.Zero(t, client.writes)
			default:
				require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
				require.NotEqual(t, client.owner, client.principal)
				require.Zero(t, client.writes)
			}
		})
	}
}
