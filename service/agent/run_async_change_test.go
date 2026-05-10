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
	base "github.com/viant/agently-core/genai/llm/provider/base"
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

type steerAwareFinder struct {
	calls    atomic.Int32
	content  string
	requests chan *llm.GenerateRequest
}

func (f *steerAwareFinder) Find(context.Context, string) (llm.Model, error) {
	return steerAwareModel{finder: f}, nil
}

type steerAwareModel struct {
	finder *steerAwareFinder
}

func (m steerAwareModel) Generate(_ context.Context, req *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	m.finder.calls.Add(1)
	if m.finder.requests != nil {
		select {
		case m.finder.requests <- req:
		default:
		}
	}
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

func (steerAwareModel) Implements(feature string) bool {
	return feature == base.CanUseTools
}

func TestServiceRunPlanLoop_SteerUnlocksAsyncWaitAndAddsDirective(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	conv := &apiconv.Conversation{
		Id: "conv-steer",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-steer",
				Status:         "running",
				Message: []*agconv.MessageView{
					{
						Id:             "user-1",
						ConversationId: "conv-steer",
						Role:           "user",
						Type:           "text",
						Mode:           cancelPtr("task"),
						Content:        cancelPtr("initial ask"),
						TurnId:         cancelPtr("turn-1"),
						CreatedAt:      now,
					},
				},
			},
		},
	}
	convClient := newLoopConvClient(conv)
	finder := &steerAwareFinder{
		content:  "final answer",
		requests: make(chan *llm.GenerateRequest, 1),
	}
	reg := &fakeRegistry{defs: []llm.ToolDefinition{
		{Name: "message/add", Description: "Add an assistant message to the current turn."},
	}}
	llmSvc := core.New(finder, reg, convClient)
	manager := asynccfg.NewManager()
	svc := &Service{
		llm:          llmSvc,
		registry:     reg,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, reg, convClient, nil, nil),
		defaults:     &config.Defaults{},
		asyncManager: manager,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{
		ConversationID: "conv-steer",
		TurnID:         "turn-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1", Iteration: 1})

	manager.Register(ctx, asynccfg.RegisterInput{
		ID:            "child-1",
		ParentConvID:  "conv-steer",
		ParentTurnID:  "turn-1",
		ToolCallID:    "call-1",
		ToolMessageID: "tool-msg-1",
		ToolName:      "llm/agents:start",
		ExecutionMode: string(asynccfg.ExecutionModeWait),
		Status:        "running",
		Message:       "waiting",
	})

	input := &QueryInput{
		ConversationID: "conv-steer",
		UserId:         "user-1",
		Query:          "initial ask",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "simple"},
			ModelSelection: llm.ModelSelection{
				Model: "mock-model",
			},
			Prompt: &binding.Prompt{Text: "You are helpful."},
		},
	}
	output := &QueryOutput{}

	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.runPlanLoop(ctx, input, output)
	}()

	time.Sleep(25 * time.Millisecond)
	steerMsg := &agconv.MessageView{
		Id:             "steer-1",
		ConversationId: "conv-steer",
		Role:           "user",
		Type:           "task",
		Mode:           cancelPtr("task"),
		Content:        cancelPtr("focus only on the steer"),
		TurnId:         cancelPtr("turn-1"),
		CreatedAt:      now.Add(time.Second),
	}
	conv.Transcript[0].Message = append(conv.Transcript[0].Message, steerMsg)
	convClient.messages[steerMsg.Id] = steerMsg
	manager.SignalTurn(ctx, "conv-steer", "turn-1")

	var req *llm.GenerateRequest
	select {
	case req = <-finder.requests:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for model generate after steer")
	}

	cancel()
	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for runPlanLoop exit")
	}

	require.GreaterOrEqual(t, finder.calls.Load(), int32(1))
	var sawSteerUser bool
	var sawSteerDirective bool
	var sawStatusSnapshot bool
	for _, msg := range req.Messages {
		if strings.EqualFold(string(msg.Role), string(llm.RoleUser)) && strings.Contains(msg.Content, "focus only on the steer") {
			sawSteerUser = true
		}
		if strings.EqualFold(string(msg.Role), string(llm.RoleSystem)) &&
			strings.Contains(msg.Content, "Steering update:") &&
			strings.Contains(msg.Content, "messageId=steer-1") &&
			strings.Contains(msg.Content, "message:add") &&
			strings.Contains(msg.Content, "interim unset or false") &&
			strings.Contains(msg.Content, "continue the current turn") {
			sawSteerDirective = true
		}
		if strings.Contains(msg.Content, `"operationId":"child-1"`) && strings.Contains(msg.Content, `"opsStillActive":true`) && strings.Contains(msg.Content, `"detail":"waiting"`) {
			sawStatusSnapshot = true
		}
	}
	require.True(t, sawSteerUser, "expected steer task message in model history")
	require.True(t, sawSteerDirective, "expected explicit steering directive in model history")
	require.True(t, sawStatusSnapshot, "expected async status-so-far snapshot in model history")
	require.True(t, requestHasTool(req, "message-add"), "expected message:add to be visible as message-add in model tools")
}

func requestHasTool(req *llm.GenerateRequest, name string) bool {
	if req == nil || req.Options == nil {
		return false
	}
	for _, tool := range req.Options.Tools {
		if strings.EqualFold(strings.TrimSpace(tool.Definition.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}
