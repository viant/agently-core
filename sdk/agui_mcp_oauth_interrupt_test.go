package sdk

import (
	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/agent/execution"
	"github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/elicitation"
	elicrouter "github.com/viant/agently-core/service/elicitation/router"
	"github.com/viant/mcp-protocol/schema"
	"testing"
)

func TestAGUIInspectIncludesMCPAuthControlInterrupt(t *testing.T) {
	c := newDatlyObservedClient(t, 128)
	ctx := recoveryContext()
	record, _ := observerAdmission(t, c, observerInput())
	require.NoError(t, nativeTestTurn(ctx, c, record.TurnID, "running"))
	svc := elicitation.New(c.conv, nil, elicrouter.New(), elicitation.NoopAwaiterFactory())
	_, err := svc.Record(ctx, &requestctx.TurnMeta{ConversationID: record.ConversationID, TurnID: record.TurnID}, "control", &execution.Elicitation{ElicitRequestParams: schema.ElicitRequestParams{
		Message: "Connect Asana", ElicitationId: "asana-connect", Mode: "mcp_oauth", Url: "/v1/api/auth/mcp/asana/initiate",
	}})
	require.NoError(t, err)
	state, err := c.native.aguiInspectRun(ctx, record)
	require.NoError(t, err)
	require.Len(t, state.Pending.Interrupts, 1)
	require.Equal(t, "asana-connect", state.Pending.Interrupts[0].ID)
	require.Empty(t, state.Messages, "control messages must not become assistant text")
	pending, err := c.native.ListPendingElicitations(ctx, &ListPendingElicitationsInput{ConversationID: record.ConversationID})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "mcp_oauth", pending[0].Elicitation["mode"])
	require.Equal(t, "/v1/api/auth/mcp/asana/initiate", pending[0].Elicitation["url"])
}

func TestCanonicalMCPAuthControlPrompt(t *testing.T) {
	id, status, content := "asana-connect", "pending", "Connect Asana"
	msg := &conversationmodel.MessageView{Id: "control-1", Role: "control", Type: "control", ElicitationId: &id, Status: &status, Content: &content}
	msg.Elicitation = map[string]interface{}{"message": content, "mode": "mcp_oauth", "url": "/v1/api/auth/mcp/asana/initiate"}
	state := BuildCanonicalState("conversation", convstore.Transcript{{Id: "turn", ConversationId: "conversation", Status: "waiting_for_user", Message: []*conversationmodel.MessageView{msg}}})
	pending := pendingElicitation(state)
	require.NotNil(t, pending)
	require.Equal(t, "mcp_oauth", pending.Mode)
	require.Equal(t, "/v1/api/auth/mcp/asana/initiate", pending.URL)
}
