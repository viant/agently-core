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

type queuePrincipalClient struct {
	Client
	principal string
	input     *MoveQueuedTurnInput
}

func (c *queuePrincipalClient) MoveQueuedTurn(ctx context.Context, input *MoveQueuedTurnInput) error {
	c.principal = iauth.EffectiveUserID(ctx)
	c.input = input
	return nil
}

func TestQueuedMoveHTTPPrincipalParity(t *testing.T) {
	for _, mode := range []string{"local", "authenticated", "missing-auth"} {
		t.Run(mode, func(t *testing.T) {
			client := &queuePrincipalClient{}
			req := httptest.NewRequest("POST", "/v1/conversations/owned/turns/turn/move", strings.NewReader(`{"conversationId":"forged","turnId":"forged","direction":"up"}`))
			req.SetPathValue("id", "owned")
			req.SetPathValue("turnId", "turn")
			req.AddCookie(&http.Cookie{Name: anonymousUserCookieName, Value: "anonymous:fixture"})
			if mode == "authenticated" {
				req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "owner"}))
			}
			response := httptest.NewRecorder()
			handleMoveQueuedTurn(client, &svcauth.Config{Enabled: mode != "local"})(response, req)
			if mode == "missing-auth" {
				require.Equal(t, http.StatusUnauthorized, response.Code)
				require.Nil(t, client.input)
				return
			}
			require.Equal(t, http.StatusNoContent, response.Code)
			expected := "anonymous:fixture"
			if mode == "authenticated" {
				expected = "owner"
			}
			require.Equal(t, expected, client.principal)
			require.Equal(t, &MoveQueuedTurnInput{ConversationID: "owned", TurnID: "turn", Direction: "up"}, client.input)
		})
	}
}
