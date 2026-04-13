package sdk

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	convmem "github.com/viant/agently-core/internal/service/conversation/memory"
	queueRead "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/read"
	queueWrite "github.com/viant/agently-core/pkg/agently/toolapprovalqueue/write"
	"github.com/viant/agently-core/protocol/tool"
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
