package sdk

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	authctx "github.com/viant/agently-core/internal/auth"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	"github.com/viant/agently-core/protocol/tool"
	api "github.com/viant/agently-core/sdk/api"
)

func TestEmbeddedClient_DecideToolApproval_ApproveSystemOSEnvCompletesTurn(t *testing.T) {
	ctx := context.Background()
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, ctx, client, "conv-approve", "turn-approve", "approval-approve", "LOGNAME")

	out, err := client.DecideToolApproval(ctx, &DecideToolApprovalInput{
		ID:     row.Id,
		Action: "approve",
		UserID: "devuser",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ok", out.Status)
	require.NotNil(t, out.Outcome)
	require.Equal(t, row.Id, out.Outcome.ApprovalID)
	require.Equal(t, "approve", out.Outcome.Action)
	require.Equal(t, "executed", out.Outcome.Status)
	require.Equal(t, "approve", out.Outcome.Decision)
	require.Equal(t, "system/os/getEnv", out.Outcome.ToolName)
	require.Equal(t, `{"values":{"LOGNAME":"awitas"}}`, out.Outcome.Result)

	gotRows, err := client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		Id:  row.Id,
		Has: &queueRead.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, gotRows, 1)
	require.Equal(t, "executed", gotRows[0].Status)
	require.Equal(t, "approve", queueTestStringValue(gotRows[0].Decision))

	conv, err := client.GetConversation(ctx, "conv-approve")
	require.NoError(t, err)
	require.NotNil(t, conv)
	require.Equal(t, "succeeded", queueTestStringValue(conv.Status))
	require.Len(t, conv.Transcript, 1)
	require.Equal(t, "succeeded", conv.Transcript[0].Status)

	messages := conv.Transcript[0].Message
	require.Len(t, messages, 5)
	require.Equal(t, "assistant", messages[4].Role)
	require.Equal(t, "```json\n{\"values\":{\"LOGNAME\":\"awitas\"}}\n```", queueTestStringValue(messages[4].Content))
	require.Equal(t, 0, messages[4].Interim)
	require.Equal(t, "tool", messages[3].Role)
	require.Equal(t, "{\"values\":{\"LOGNAME\":\"awitas\"}}", queueTestStringValue(messages[3].Content))
}

func TestEmbeddedClient_DecideToolApproval_CancelSystemOSEnvCompletesTurn(t *testing.T) {
	ctx := context.Background()
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, ctx, client, "conv-cancel", "turn-cancel", "approval-cancel", "LOGNAME")

	out, err := client.DecideToolApproval(ctx, &DecideToolApprovalInput{
		ID:     row.Id,
		Action: "cancel",
		UserID: "devuser",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ok", out.Status)
	require.NotNil(t, out.Outcome)
	require.Equal(t, row.Id, out.Outcome.ApprovalID)
	require.Equal(t, "cancel", out.Outcome.Action)
	require.Equal(t, "canceled", out.Outcome.Status)
	require.Equal(t, "cancel", out.Outcome.Decision)
	require.Equal(t, "tool execution was not approved by user", out.Outcome.Result)

	gotRows, err := client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		Id:  row.Id,
		Has: &queueRead.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, gotRows, 1)
	require.Equal(t, "canceled", gotRows[0].Status)
	require.Equal(t, "cancel", queueTestStringValue(gotRows[0].Decision))

	conv, err := client.GetConversation(ctx, "conv-cancel")
	require.NoError(t, err)
	require.NotNil(t, conv)
	require.Equal(t, "succeeded", queueTestStringValue(conv.Status))
	require.Len(t, conv.Transcript, 1)
	require.Equal(t, "succeeded", conv.Transcript[0].Status)

	messages := conv.Transcript[0].Message
	require.Len(t, messages, 5)
	require.Equal(t, "assistant", messages[4].Role)
	require.Equal(t, "I couldn't retrieve your LOGNAME environment variable because approval was not granted.", queueTestStringValue(messages[4].Content))
	require.Equal(t, 0, messages[4].Interim)
	require.Equal(t, "tool", messages[3].Role)
	require.Equal(t, "tool execution was not approved by user", queueTestStringValue(messages[3].Content))
}

func TestEmbeddedClient_ListPendingToolApprovals_UsesEffectiveUserScope(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	_ = seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-scope", "turn-scope", "approval-scope", "LOGNAME")

	out, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{Status: "pending"})
	require.NoError(t, err)
	require.Len(t, out.Rows, 1)
	require.Equal(t, "devuser", out.Rows[0].UserID)

	_, err = client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{UserID: "other-user", Status: "pending"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission denied")
}

func TestEmbeddedClient_DecideToolApproval_DeniesCrossUserQueueAccess(t *testing.T) {
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-owner", "turn-owner", "approval-owner", "LOGNAME")

	_, err := client.DecideToolApproval(
		authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "other-user"}),
		&DecideToolApprovalInput{
			ID:     row.Id,
			Action: "approve",
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "approval request not found")
}

func TestEmbeddedClient_ListPendingToolApprovals_ExpiresTimedOutRowsAndReturnsOutcome(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-timeout", "turn-timeout", "approval-timeout", "LOGNAME")
	expiredAt := time.Now().UTC().Add(-1 * time.Minute)
	queue := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	queue.SetId(row.Id)
	queue.SetUserId("devuser")
	queue.SetToolName("system/os/getEnv")
	queue.SetArguments([]byte(`{"names":["LOGNAME"]}`))
	queue.SetExpiresAt(expiredAt)
	queue.SetUpdatedAt(time.Now().UTC())
	require.NoError(t, client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(context.Background(), queue))

	out, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{Status: "pending"})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Len(t, out.Rows, 0, "expired rows must not remain in pending page")
	require.Len(t, out.Outcomes, 1, "timeout producer must return one canonical outcome")
	require.Equal(t, "timeout", out.Outcomes[0].Action)
	require.Equal(t, "timed_out", out.Outcomes[0].Status)
	require.Equal(t, api.ApprovalTimeoutErrorMessage, out.Outcomes[0].ErrorMessage)

	gotRows, err := client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		Id:  row.Id,
		Has: &queueRead.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, gotRows, 1)
	require.Equal(t, "timed_out", gotRows[0].Status)
}

// TestEmbeddedClient_DecideToolApproval_ExpiredPendingRowYieldsTimeoutOutcome
// guards the canonical race where a user clicks approve after the
// deadline has already passed. The decide path must produce the
// canonical timeout outcome (Action="timeout", Status="timed_out",
// Decision="timeout"), not silently overwrite the row with the late
// user action.
func TestEmbeddedClient_DecideToolApproval_ExpiredPendingRowYieldsTimeoutOutcome(t *testing.T) {
	ctx := context.Background()
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, ctx, client, "conv-expired", "turn-expired", "approval-expired", "LOGNAME")
	expired := time.Now().UTC().Add(-2 * time.Minute)
	patch := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	patch.SetId(row.Id)
	patch.SetUserId("devuser")
	patch.SetToolName("system/os/getEnv")
	patch.SetArguments(row.Arguments)
	patch.SetExpiresAt(expired)
	patch.SetUpdatedAt(time.Now().UTC())
	require.NoError(t, client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, patch))

	out, err := client.DecideToolApproval(ctx, &DecideToolApprovalInput{
		ID:     row.Id,
		Action: "approve",
		UserID: "devuser",
	})
	require.NoError(t, err)
	require.NotNil(t, out)
	require.Equal(t, "ok", out.Status)
	require.NotNil(t, out.Outcome)
	require.Equal(t, api.ApprovalTimeoutOutcomeAction, out.Outcome.Action)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, out.Outcome.Status)
	require.Equal(t, api.ApprovalTimeoutOutcomeDecision, out.Outcome.Decision)
	require.Equal(t, api.ApprovalTimeoutErrorMessage, out.Outcome.ErrorMessage)
	require.NotNil(t, out.Outcome.TimedOutAt)
	require.NotNil(t, out.Outcome.ExpiresAt)

	gotRows, err := client.conv.(toolApprovalQueueLister).ListToolApprovalQueues(ctx, &queueRead.QueueRowsInput{
		Id:  row.Id,
		Has: &queueRead.QueueRowsInputHas{Id: true},
	})
	require.NoError(t, err)
	require.Len(t, gotRows, 1)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, gotRows[0].Status)
	require.NotNil(t, gotRows[0].Decision)
	require.Equal(t, api.ApprovalTimeoutOutcomeDecision, *gotRows[0].Decision)
	require.NotNil(t, gotRows[0].TimedOutAt)
}

// TestEmbeddedClient_ListPendingToolApprovals_DurableTimeoutOutcomeViaCursor
// proves the durable outcome cursor contract for the sweep-driven
// timeout path. The first poll runs the canonical timeout sweep,
// emits the outcome, and returns a non-empty OutcomeCursor captured
// at the start of the request (i.e. before the row's terminal
// transition). A second poll that carries that same cursor must
// still observe the timeout outcome — synthesized from the persisted
// row, not from a one-shot in-memory queue — so a client that polls
// just after the transition moment is not silently dropped from the
// outcome propagation path.
func TestEmbeddedClient_ListPendingToolApprovals_DurableTimeoutOutcomeViaCursor(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-cursor-timeout", "turn-cursor-timeout", "approval-cursor-timeout", "LOGNAME")
	expiredAt := time.Now().UTC().Add(-1 * time.Minute)
	queue := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	queue.SetId(row.Id)
	queue.SetUserId("devuser")
	queue.SetToolName("system/os/getEnv")
	queue.SetArguments([]byte(`{"names":["LOGNAME"]}`))
	queue.SetExpiresAt(expiredAt)
	queue.SetUpdatedAt(time.Now().UTC())
	require.NoError(t, client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(context.Background(), queue))

	first, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{Status: "pending"})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Len(t, first.Outcomes, 1, "first poll must emit canonical sweep outcome")
	require.Equal(t, api.ApprovalTimeoutOutcomeAction, first.Outcomes[0].Action)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, first.Outcomes[0].Status)
	require.NotEmpty(t, first.OutcomeCursor, "first poll must return a non-empty outcome cursor")

	time.Sleep(2 * time.Millisecond)

	second, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{
		Status:       "pending",
		OutcomeSince: first.OutcomeCursor,
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Len(t, second.Outcomes, 1, "outcome must remain observable on a poll that carries the prior cursor")
	require.Equal(t, row.Id, second.Outcomes[0].ApprovalID)
	require.Equal(t, api.ApprovalTimeoutOutcomeAction, second.Outcomes[0].Action)
	require.Equal(t, api.ApprovalTimeoutOutcomeStatus, second.Outcomes[0].Status)
	require.Equal(t, api.ApprovalTimeoutOutcomeDecision, second.Outcomes[0].Decision)
	require.Equal(t, api.ApprovalTimeoutErrorMessage, second.Outcomes[0].ErrorMessage)
	require.NotEmpty(t, second.OutcomeCursor, "second poll must echo a fresh cursor")

	time.Sleep(2 * time.Millisecond)

	third, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{
		Status:       "pending",
		OutcomeSince: second.OutcomeCursor,
	})
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Empty(t, third.Outcomes, "advancing past the row's transition time must stop re-emitting the outcome")
}

// TestEmbeddedClient_ListPendingToolApprovals_DurableApproveOutcomeViaCursor
// proves the same durability contract for an interactive
// approve-execute decision. The decision happens between the first
// and second polls. The second poll, carrying the cursor captured
// before the decision moment, must still surface the canonical
// approve/executed outcome rather than missing it.
func TestEmbeddedClient_ListPendingToolApprovals_DurableApproveOutcomeViaCursor(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-cursor-approve", "turn-cursor-approve", "approval-cursor-approve", "LOGNAME")

	first, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{Status: "pending"})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Empty(t, first.Outcomes, "pending row must not produce an outcome before any decision")
	require.NotEmpty(t, first.OutcomeCursor)
	cursor := first.OutcomeCursor

	time.Sleep(2 * time.Millisecond)

	decided, err := client.DecideToolApproval(ctx, &DecideToolApprovalInput{
		ID:     row.Id,
		Action: "approve",
		UserID: "devuser",
	})
	require.NoError(t, err)
	require.NotNil(t, decided)
	require.Equal(t, "ok", decided.Status)
	require.NotNil(t, decided.Outcome)
	require.Equal(t, "executed", decided.Outcome.Status)

	time.Sleep(2 * time.Millisecond)

	second, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{
		Status:       "pending",
		OutcomeSince: cursor,
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Len(t, second.Outcomes, 1, "decision outcome must remain observable when polled after the transition moment")
	require.Equal(t, row.Id, second.Outcomes[0].ApprovalID)
	require.Equal(t, "approve", second.Outcomes[0].Action)
	require.Equal(t, "executed", second.Outcomes[0].Status)
	require.NotEmpty(t, second.OutcomeCursor)

	time.Sleep(2 * time.Millisecond)

	third, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{
		Status:       "pending",
		OutcomeSince: second.OutcomeCursor,
	})
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Empty(t, third.Outcomes, "advancing past the decision instant must stop re-emitting the outcome")
}

// TestEmbeddedClient_ListPendingToolApprovals_BootstrapPollDoesNotReplayHistory
// proves that a fresh client that has not yet captured an outcome
// cursor does not get flooded with terminal rows that were already
// resolved before it started polling. Bootstrap returns only the
// in-call outcomes (none in this case) and a fresh cursor.
func TestEmbeddedClient_ListPendingToolApprovals_BootstrapPollDoesNotReplayHistory(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "devuser"})
	client := newQueueApprovalTestClient(t, `{"values":{"LOGNAME":"awitas"}}`)
	row := seedPendingSystemOSEnvApproval(t, context.Background(), client, "conv-bootstrap", "turn-bootstrap", "approval-bootstrap", "LOGNAME")
	decisionTime := time.Now().UTC().Add(-1 * time.Minute)
	patch := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	patch.SetId(row.Id)
	patch.SetUserId("devuser")
	patch.SetToolName("system/os/getEnv")
	patch.SetArguments(row.Arguments)
	patch.SetStatus("executed")
	patch.SetDecision("approve")
	patch.SetApprovedAt(decisionTime)
	patch.SetExecutedAt(decisionTime)
	patch.SetUpdatedAt(decisionTime)
	require.NoError(t, client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(context.Background(), patch))

	first, err := client.ListPendingToolApprovals(ctx, &ListPendingToolApprovalsInput{Status: "pending"})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Empty(t, first.Outcomes, "bootstrap poll must not replay historical outcomes")
	require.NotEmpty(t, first.OutcomeCursor)
}

func newQueueApprovalTestClient(t *testing.T, toolResult string) *backendClient {
	t.Helper()

	convClient := convmem.New()

	return &backendClient{
		conv: convClient,
		registry: &stubRegistry{
			result: toolResult,
		},
	}
}

func seedPendingSystemOSEnvApproval(t *testing.T, ctx context.Context, client *backendClient, conversationID, turnID, approvalID, envName string) *queueWrite.ToolApprovalQueue {
	t.Helper()

	conv := conversation.NewConversation()
	conv.SetId(conversationID)
	conv.SetStatus("waiting_for_user")
	require.NoError(t, client.conv.PatchConversations(ctx, conv))

	turn := conversation.NewTurn()
	turn.SetId(turnID)
	turn.SetConversationID(conversationID)
	turn.SetStatus("waiting_for_user")
	turn.SetStartedByMessageID(turnID)
	require.NoError(t, client.conv.PatchTurn(ctx, turn))

	userMsg := conversation.NewMessage()
	userMsg.SetId(turnID)
	userMsg.SetConversationID(conversationID)
	userMsg.SetTurnID(turnID)
	userMsg.SetRole("user")
	userMsg.SetType("text")
	userMsg.SetContent("What is my LOGNAME environment variable?")
	userMsg.SetCreatedByUserID("devuser")
	require.NoError(t, client.conv.PatchMessage(ctx, userMsg))

	assistant := conversation.NewMessage()
	assistant.SetId("assistant-" + turnID)
	assistant.SetConversationID(conversationID)
	assistant.SetTurnID(turnID)
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("I will read the LOGNAME environment variable using the functions.system_os-getEnv tool.")
	assistant.SetInterim(1)
	assistant.SetParentMessageID(turnID)
	assistant.SetIteration(1)
	require.NoError(t, client.conv.PatchMessage(ctx, assistant))

	queuedTool := conversation.NewMessage()
	queuedTool.SetId("queued-tool-" + turnID)
	queuedTool.SetConversationID(conversationID)
	queuedTool.SetTurnID(turnID)
	queuedTool.SetRole("tool")
	queuedTool.SetType("tool_op")
	queuedTool.SetToolName("system/os/getEnv")
	queuedTool.SetStatus("queued")
	queuedTool.SetContent("queued for user approval")
	queuedTool.SetParentMessageID(assistant.Id)
	queuedTool.SetIteration(1)
	require.NoError(t, client.conv.PatchMessage(ctx, queuedTool))

	now := time.Now().UTC()
	queue := &queueWrite.ToolApprovalQueue{Has: &queueWrite.ToolApprovalQueueHas{}}
	queue.SetId(approvalID)
	queue.SetUserId("devuser")
	queue.SetConversationId(conversationID)
	queue.SetTurnId(turnID)
	queue.SetMessageId(turnID)
	queue.SetToolName("system/os/getEnv")
	queue.SetTitle("OS Env Access")
	queue.SetArguments([]byte(`{"names":["` + envName + `"]}`))
	queue.SetMetadata([]byte(`{"opId":"call-test","responseId":"resp-test","turnId":"` + turnID + `"}`))
	queue.SetStatus("pending")
	queue.SetCreatedAt(now)
	queue.SetUpdatedAt(now)
	require.NoError(t, client.conv.(toolApprovalQueuePatcher).PatchToolApprovalQueue(ctx, queue))

	return queue
}

type stubRegistry struct {
	result string
}

func (s *stubRegistry) Definitions() []llm.ToolDefinition                     { return nil }
func (s *stubRegistry) MatchDefinition(pattern string) []*llm.ToolDefinition  { return nil }
func (s *stubRegistry) GetDefinition(name string) (*llm.ToolDefinition, bool) { return nil, false }
func (s *stubRegistry) MustHaveTools(patterns []string) ([]llm.Tool, error)   { return nil, nil }
func (s *stubRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return s.result, nil
}
func (s *stubRegistry) SetDebugLogger(w io.Writer)     {}
func (s *stubRegistry) Initialize(ctx context.Context) {}

func queueTestStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

var _ tool.Registry = (*stubRegistry)(nil)
