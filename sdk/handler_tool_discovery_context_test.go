package sdk

import (
	"context"
	iauth "github.com/viant/agently-core/internal/auth"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
	"net/http/httptest"
	"testing"
)

type toolDiscoveryContextClient struct {
	Client
	ctx context.Context
}

func (c *toolDiscoveryContextClient) ListToolDefinitions(ctx context.Context) ([]ToolDefinitionInfo, error) {
	c.ctx = ctx
	return nil, nil
}
func TestListToolDefinitionsPreservesConversationAndAuthenticatedPrincipal(t *testing.T) {
	for _, subject := range []string{"", "authenticated-user"} {
		c := &toolDiscoveryContextClient{}
		req := httptest.NewRequest("GET", "/v1/tools?conversationId=other-owner-conversation", nil)
		ctx := req.Context()
		if subject != "" {
			ctx = iauth.WithUserInfo(ctx, &iauth.UserInfo{Subject: subject})
		}
		out := httptest.NewRecorder()
		handleListToolDefinitions(c)(out, req.WithContext(ctx))
		if out.Code != 200 {
			t.Fatal(out.Code)
		}
		if got := requestctx.ConversationIDFromContext(c.ctx); got != "other-owner-conversation" {
			t.Fatalf("conversation %q", got)
		}
		if got := iauth.EffectiveUserID(c.ctx); got != subject {
			t.Fatalf("scope substituted authentication: %q", got)
		}
	}
}
