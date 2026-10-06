package sdk

import (
	"context"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	agentsvc "github.com/viant/agently-core/service/agent"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type approvalAwareResumeClient struct{ *durableAGUIClient }

func (c *approvalAwareResumeClient) aguiCompleteResumeWithApprovalReceipts(ctx context.Context, prior *aguistore.Run, input *agui.RunAgentInput, pending aguiPending) error {
	return (&backendClient{}).aguiCompleteResumeWithApprovalReceipts(ctx, prior, input, pending)
}
func TestAGUIApprovalAwareFrontendResultContinuationWithoutResumeAndExactReplay(t *testing.T) {
	base, _ := newDurableAGUIServer(t)
	client := &approvalAwareResumeClient{base}
	client.query = func(_ context.Context, input *agentsvc.QueryInput) (*agentsvc.QueryOutput, error) {
		return &agentsvc.QueryOutput{ClientToolCalls: []clienttool.PendingCall{{ID: "call", Name: "browser", Arguments: map[string]any{}, ConversationID: input.ConversationID, TurnID: input.MessageID, AssistantMessageID: "assistant", ToolMessageID: "native-tool"}}}, nil
	}
	handler := handleAGUIRun(client, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(w, r.WithContext(iauth.WithUserInfo(r.Context(), &iauth.UserInfo{Subject: "owner"})))
	}))
	defer server.Close()
	status, wire := durablePost(t, server, aguiChatRequest, nil)
	require.Equal(t, 200, status, wire)
	original, err := client.store.GetRun(context.Background(), "owner", "thread", "external-run")
	require.NoError(t, err)
	alias := agui.ProtocolToolCallID(original.TurnID, "call")
	body := string(rawAGUI(map[string]any{"threadId": "thread", "runId": "continued", "messages": []any{map[string]any{"id": "client-result", "role": "tool", "toolCallId": alias, "content": "real frontend result"}}}))
	require.NotContains(t, body, `"resume"`)
	status, wire = durablePost(t, server, body, nil)
	require.Equal(t, 200, status, wire)
	require.Contains(t, wire, "continued answer")
	status, replay := durablePost(t, server, body, nil)
	require.Equal(t, 200, status, replay)
	require.Equal(t, wire, replay)
	require.EqualValues(t, 1, client.queries.Load())
	require.EqualValues(t, 1, client.resumes.Load())
	require.EqualValues(t, 1, client.completions.Load())
	require.NotContains(t, strings.ToLower(wire), "unexpected end of json")
}
