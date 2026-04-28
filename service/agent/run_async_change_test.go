package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

type singleResponseFinder struct {
	calls   atomic.Int32
	content string
}

func (f *singleResponseFinder) Find(context.Context, string) (llm.Model, error) {
	return singleResponseModel{finder: f}, nil
}

type singleResponseModel struct {
	finder *singleResponseFinder
}

func (m singleResponseModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.calls.Add(1)
	return &llm.GenerateResponse{
		Choices: []llm.Choice{
			{
				Index: 0,
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: m.finder.content,
				},
				FinishReason: "stop",
			},
		},
		Model: "mock-model",
	}, nil
}

func (singleResponseModel) Implements(string) bool { return false }

type loopConvClient struct {
	conversation *apiconv.Conversation
	messages     map[string]*agconv.MessageView
}

func newLoopConvClient(conv *apiconv.Conversation) *loopConvClient {
	index := map[string]*agconv.MessageView{}
	if conv != nil {
		for _, turn := range conv.Transcript {
			if turn == nil {
				continue
			}
			for _, msg := range turn.Message {
				if msg == nil {
					continue
				}
				index[msg.Id] = msg
			}
		}
	}
	return &loopConvClient{conversation: conv, messages: index}
}

func (c *loopConvClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return c.conversation, nil
}

func (c *loopConvClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (c *loopConvClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (c *loopConvClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (c *loopConvClient) PatchPayload(context.Context, *apiconv.MutablePayload) error { return nil }
func (c *loopConvClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (c *loopConvClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (c *loopConvClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error { return nil }
func (c *loopConvClient) PatchTurn(context.Context, *apiconv.MutableTurn) error         { return nil }
func (c *loopConvClient) DeleteConversation(context.Context, string) error              { return nil }
func (c *loopConvClient) DeleteMessage(context.Context, string, string) error           { return nil }

func (c *loopConvClient) PatchModelCall(_ context.Context, in *apiconv.MutableModelCall) error {
	if in == nil || in.Has == nil || !in.Has.MessageID {
		return nil
	}
	msg, ok := c.messages[in.MessageID]
	if !ok {
		msg = &agconv.MessageView{Id: in.MessageID, ConversationId: c.conversation.Id, Role: "assistant", Type: "text", CreatedAt: time.Now()}
		c.messages[in.MessageID] = msg
		c.conversation.Transcript[0].Message = append(c.conversation.Transcript[0].Message, msg)
	}
	if msg.ModelCall == nil {
		msg.ModelCall = &agconv.ModelCallView{MessageId: in.MessageID}
	}
	if in.Has.Status {
		msg.ModelCall.Status = in.Status
	}
	if in.Has.CompletedAt {
		msg.ModelCall.CompletedAt = in.CompletedAt
	}
	return nil
}

func (c *loopConvClient) PatchMessage(_ context.Context, in *apiconv.MutableMessage) error {
	if in == nil || in.Has == nil || !in.Has.Id {
		return nil
	}
	msg, ok := c.messages[in.Id]
	if !ok {
		msg = &agconv.MessageView{
			Id:             in.Id,
			ConversationId: c.conversation.Id,
			Role:           "assistant",
			Type:           "text",
			CreatedAt:      time.Now(),
			TurnId:         cancelPtr("turn-1"),
		}
		c.messages[in.Id] = msg
		c.conversation.Transcript[0].Message = append(c.conversation.Transcript[0].Message, msg)
	}
	if in.Has.ConversationID {
		msg.ConversationId = in.ConversationID
	}
	if in.Has.TurnID {
		msg.TurnId = in.TurnID
	}
	if in.Has.Role {
		msg.Role = in.Role
	}
	if in.Has.Type {
		msg.Type = in.Type
	}
	if in.Has.Mode {
		msg.Mode = in.Mode
	}
	if in.Has.Content {
		msg.Content = in.Content
	}
	if in.Has.Interim && in.Interim != nil {
		msg.Interim = *in.Interim
	}
	if in.Has.Iteration {
		msg.Iteration = in.Iteration
	}
	return nil
}

func TestServiceRunPlanLoop_TerminalContentIsNotDiscardedByCompletedAsyncChange(t *testing.T) {
	t.Parallel()

	conv := &apiconv.Conversation{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-1",
				Message: []*agconv.MessageView{
					{
						Id:             "user-1",
						ConversationId: "conv-1",
						Role:           "user",
						Type:           "text",
						Mode:           cancelPtr("task"),
						Content:        cancelPtr("hello"),
						TurnId:         cancelPtr("turn-1"),
					},
				},
			},
		},
	}
	convClient := newLoopConvClient(conv)
	finder := &singleResponseFinder{content: "final answer"}
	llmSvc := core.New(finder, nil, convClient)
	manager := asynccfg.NewManager()
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
		asyncManager: manager,
	}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 1})

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "child-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolCallID:    "call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "completed",
		Message:       "child completed",
	})

	input := &QueryInput{
		ConversationID: "conv-1",
		UserId:         "user-1",
		Query:          "hello",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "simple"},
			ModelSelection: llm.ModelSelection{
				Model: "mock-model",
			},
			Prompt: &binding.Prompt{Text: "You are helpful."},
		},
	}
	output := &QueryOutput{}

	err := svc.runPlanLoop(ctx, input, output)
	require.NoError(t, err)
	require.Equal(t, "final answer", output.Content)
	require.EqualValues(t, 1, finder.calls.Load(), "terminal content-only answer should not trigger a second same-turn model pass from residual completed async changes")
	var finalMsg *agconv.MessageView
	for _, msg := range conv.Transcript[0].Message {
		if msg == nil || !strings.EqualFold(msg.Role, "assistant") || msg.Content == nil {
			continue
		}
		if strings.TrimSpace(*msg.Content) == "final answer" {
			finalMsg = msg
		}
	}
	require.NotNil(t, finalMsg)
	require.Equal(t, 0, finalMsg.Interim, "terminal content-only answer should be finalized with interim=0")
}
