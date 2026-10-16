package sdk

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	iauth "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	svcauth "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/elicitation"
	"github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/mcp-protocol/schema"
)

type nativeElicitationHTTPClient struct {
	Client
	service     *elicitation.Service
	resolutions int
}

type wrappedElicitationErrorClient struct {
	Client
	err error
}

func (c wrappedElicitationErrorClient) ResolveElicitation(context.Context, *ResolveElicitationInput) error {
	return c.err
}

func TestResolveElicitationHTTPClassifiesOnlyTypedErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"wrapped missing", fmt.Errorf("native reader: %w", elicitation.ErrNotFound), http.StatusNotFound},
		{"wrapped conflict", fmt.Errorf("native transaction: %w", elicitation.ErrConflict), http.StatusConflict},
		{"untyped infrastructure", fmt.Errorf("upstream object not found during execution"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/resolve", strings.NewReader(`{"action":"accept"}`))
			req.SetPathValue("conversationId", "owned")
			req.SetPathValue("elicitationId", "ask")
			req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: "owner"}))
			response := httptest.NewRecorder()
			handleResolveElicitation(wrappedElicitationErrorClient{err: tc.err}, &svcauth.Config{Enabled: true})(response, req)
			require.Equal(t, tc.status, response.Code)
		})
	}
}

func (c *nativeElicitationHTTPClient) ResolveElicitation(ctx context.Context, in *ResolveElicitationInput) error {
	c.resolutions++
	_, err := c.service.ResolveChecked(ctx, in.ConversationID, in.ElicitationID, in.Action, in.Payload, "")
	return err
}

func TestResolveElicitationHTTPNativeErrorsDoNotWakeOrDispatch(t *testing.T) {
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	root := apiconv.NewConversation()
	root.SetId("owned")
	root.SetCreatedByUserID("owner")
	require.NoError(t, conv.PatchConversations(ctx, root))
	turn := apiconv.NewTurn()
	turn.SetId("turn")
	turn.SetConversationID("owned")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	routed := router.New()
	service := elicitation.New(conv, nil, routed, nil)
	expired, err := service.Record(ctx, &requestctx.TurnMeta{ConversationID: "owned", TurnID: "turn"}, "assistant", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Expired request", ElicitationId: "expired"}})
	require.NoError(t, err)
	expired.SetStatus("expired")
	require.NoError(t, conv.PatchMessage(ctx, expired))
	_, err = service.Record(ctx, &requestctx.TurnMeta{ConversationID: "owned", TurnID: "turn"}, "assistant", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Choose a color", ElicitationId: "accepted"}})
	require.NoError(t, err)
	_, err = service.ResolveChecked(ctx, "owned", "accepted", "accept", map[string]interface{}{"color": "red"}, "")
	require.NoError(t, err)
	_, err = service.Record(ctx, &requestctx.TurnMeta{ConversationID: "owned", TurnID: "turn"}, "assistant", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{Message: "Late answer still accepted", ElicitationId: "late"}, Deadline: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	require.NoError(t, err)
	client := &nativeElicitationHTTPClient{service: service}
	for _, tc := range []struct {
		name, principal, conversation, id, payload string
		status                                     int
	}{
		{"missing", "owner", "owned", "missing", `{"color":"red"}`, http.StatusNotFound},
		{"expired", "owner", "owned", "expired", `{"color":"red"}`, http.StatusConflict},
		{"different accepted answer", "owner", "owned", "accepted", `{"color":"blue"}`, http.StatusConflict},
		{"late pending answer", "owner", "owned", "late", `{"color":"red"}`, http.StatusOK},
		{"exact accepted replay", "owner", "owned", "accepted", `{"color":"red"}`, http.StatusOK},
		{"wrong owner cannot discover missing", "other", "owned", "missing", `{"color":"red"}`, http.StatusForbidden},
		{"wrong owner cannot discover accepted", "other", "owned", "accepted", `{"color":"red"}`, http.StatusForbidden},
		{"unknown conversation remains opaque", "owner", "unknown", "missing", `{}`, http.StatusForbidden},
		{"unauthenticated", "", "owned", "accepted", `{}`, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wake := make(chan *schema.ElicitResult, 1)
			if tc.id != "accepted" || tc.status != http.StatusOK {
				routed.RegisterByElicitationID(tc.conversation, tc.id, wake)
				defer routed.RemoveByElicitation(tc.conversation, tc.id)
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/elicitations/"+tc.conversation+"/"+tc.id+"/resolve", strings.NewReader(`{"action":"accept","payload":`+tc.payload+`}`))
			req.SetPathValue("conversationId", tc.conversation)
			req.SetPathValue("elicitationId", tc.id)
			if tc.principal != "" {
				req = req.WithContext(iauth.WithUserInfo(req.Context(), &iauth.UserInfo{Subject: tc.principal}))
			}
			response := httptest.NewRecorder()
			before := client.resolutions
			handleResolveElicitation(client, &svcauth.Config{Enabled: true})(response, req)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.principal == "" {
				require.Equal(t, before, client.resolutions, "unauthenticated requests cannot reach resolution")
			} else {
				require.Equal(t, before+1, client.resolutions)
			}
			select {
			case <-wake:
				require.Equal(t, "late", tc.id, "rejected resolution cannot wake an execution")
			default:
				require.NotEqual(t, "late", tc.id, "a valid late pending answer still wakes its execution")
			}
			stored, readErr := conv.GetConversation(ctx, "owned", apiconv.WithIncludeTranscript(true), apiconv.WithIncludeToolCall(true))
			require.NoError(t, readErr)
			for _, turn := range stored.GetTranscript() {
				for _, message := range turn.GetMessages() {
					require.Nil(t, message.MessageToolCall, "resolution cannot dispatch or create a new tool call")
				}
			}
			receipt, err := service.InspectResolution(ctx, "owned", "accepted", "accept", map[string]interface{}{"color": "red"}, "")
			require.NoError(t, err)
			require.NotNil(t, receipt)
		})
	}
	for _, id := range []string{"missing", "expired"} {
		receipt, err := service.InspectResolution(ctx, "owned", id, "accept", map[string]interface{}{"color": "red"}, "")
		require.NoError(t, err)
		require.Nil(t, receipt, "rejected resolution cannot create a receipt")
	}
}
