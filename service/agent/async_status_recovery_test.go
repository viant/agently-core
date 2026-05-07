package agent

import (
	"context"
	"io"
	"testing"
	"time"

	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/tool"
	runtimerecovery "github.com/viant/agently-core/runtime/recovery"
)

type asyncStatusRecoveryRegistry struct {
	tool.Registry
	result string
	calls  []string
}

func (r *asyncStatusRecoveryRegistry) Definitions() []llm.ToolDefinition            { return nil }
func (r *asyncStatusRecoveryRegistry) MatchDefinition(string) []*llm.ToolDefinition { return nil }
func (r *asyncStatusRecoveryRegistry) GetDefinition(string) (*llm.ToolDefinition, bool) {
	return nil, false
}
func (r *asyncStatusRecoveryRegistry) MustHaveTools([]string) ([]llm.Tool, error) { return nil, nil }
func (r *asyncStatusRecoveryRegistry) SetDebugLogger(io.Writer)                   {}
func (r *asyncStatusRecoveryRegistry) Initialize(context.Context)                 {}
func (r *asyncStatusRecoveryRegistry) Execute(_ context.Context, name string, _ map[string]interface{}) (string, error) {
	r.calls = append(r.calls, name)
	return r.result, nil
}
func (r *asyncStatusRecoveryRegistry) ToolTimeout(string) (time.Duration, bool) { return 0, false }

type asyncStatusRecoveryConvClient struct {
	conversations map[string]*apiconv.Conversation
	messages      []*apiconv.MutableMessage
	toolCalls     []*apiconv.MutableToolCall
	payloads      []*apiconv.MutablePayload
}

func (c *asyncStatusRecoveryConvClient) GetConversation(_ context.Context, id string, _ ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conversations[id], nil
}
func (c *asyncStatusRecoveryConvClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (c *asyncStatusRecoveryConvClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (c *asyncStatusRecoveryConvClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (c *asyncStatusRecoveryConvClient) PatchPayload(_ context.Context, payload *apiconv.MutablePayload) error {
	c.payloads = append(c.payloads, payload)
	return nil
}
func (c *asyncStatusRecoveryConvClient) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	c.messages = append(c.messages, message)
	return nil
}
func (c *asyncStatusRecoveryConvClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (c *asyncStatusRecoveryConvClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (c *asyncStatusRecoveryConvClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (c *asyncStatusRecoveryConvClient) PatchToolCall(_ context.Context, toolCall *apiconv.MutableToolCall) error {
	c.toolCalls = append(c.toolCalls, toolCall)
	return nil
}
func (c *asyncStatusRecoveryConvClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}
func (c *asyncStatusRecoveryConvClient) DeleteConversation(context.Context, string) error {
	return nil
}
func (c *asyncStatusRecoveryConvClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func TestService_RepairResumedAsyncStatusRows_CompletesTerminalChildCarrier(t *testing.T) {
	now := time.Now().UTC()
	parentID := "parent-conv"
	childID := "child-1"
	requestPayload := `{"conversationId":"child-1"}`
	terminalResult := `{"conversationId":"child-1","status":"succeeded","hasFinalResponse":true,"message":"done"}`

	parent := &apiconv.Conversation{
		Id:     parentID,
		Status: strPtrRecovery("running"),
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-old",
				ConversationId: parentID,
				Status:         "failed",
				CreatedAt:      now,
				Message: []*agconv.MessageView{
					{
						Id:             "tool-msg-1",
						ConversationId: parentID,
						TurnId:         strPtrRecovery("turn-old"),
						Role:           "tool",
						Type:           "tool_op",
						Content:        strPtrRecovery(`{"conversationId":"child-1","status":"running"}`),
						CreatedAt:      now.Add(time.Second),
						MessageToolCall: &agconv.MessageToolCallView{
							MessageId:        "tool-msg-1",
							TurnId:           strPtrRecovery("turn-old"),
							OpId:             "async-status:child-1",
							Attempt:          1,
							ToolName:         "llm/agents:status",
							ToolKind:         "tool",
							Status:           "running",
							RequestPayloadId: strPtrRecovery("req-1"),
							MessageRequestPayload: &agconv.ModelCallStreamPayloadView{
								Id:         "req-1",
								InlineBody: strPtrRecovery(requestPayload),
							},
						},
					},
				},
			},
		},
	}

	child := &apiconv.Conversation{
		Id:     childID,
		Status: strPtrRecovery("succeeded"),
	}

	convClient := &asyncStatusRecoveryConvClient{
		conversations: map[string]*apiconv.Conversation{
			parentID: parent,
			childID:  child,
		},
	}
	reg := &asyncStatusRecoveryRegistry{result: terminalResult}
	svc := &Service{
		conversation: convClient,
		registry:     reg,
	}

	ctx := runtimerecovery.WithMode(context.Background(), runtimerecovery.ModeResume)
	err := svc.repairResumedAsyncStatusRows(ctx, &QueryInput{ConversationID: parentID})
	if err != nil {
		t.Fatalf("repairResumedAsyncStatusRows() error = %v", err)
	}
	if len(reg.calls) != 1 || reg.calls[0] != "llm/agents:status" {
		t.Fatalf("registry calls = %#v, want one llm/agents:status", reg.calls)
	}
	if len(convClient.messages) != 1 {
		t.Fatalf("patched messages = %d, want 1", len(convClient.messages))
	}
	if got := valueOrEmpty(convClient.messages[0].Content); got != terminalResult {
		t.Fatalf("patched message content = %q, want %q", got, terminalResult)
	}
	if got := valueOrEmpty(convClient.messages[0].Status); got != "completed" {
		t.Fatalf("patched message status = %q, want completed", got)
	}
	if len(convClient.toolCalls) != 1 {
		t.Fatalf("patched tool calls = %d, want 1", len(convClient.toolCalls))
	}
	if convClient.toolCalls[0].Status != "completed" {
		t.Fatalf("patched tool call status = %q, want completed", convClient.toolCalls[0].Status)
	}
	if convClient.toolCalls[0].CompletedAt == nil || convClient.toolCalls[0].ResponsePayloadID == nil {
		t.Fatalf("patched tool call missing completedAt/responsePayloadID: %#v", convClient.toolCalls[0])
	}
	if len(convClient.payloads) != 1 {
		t.Fatalf("patched payloads = %d, want 1", len(convClient.payloads))
	}
}

func strPtrRecovery(v string) *string { return &v }
