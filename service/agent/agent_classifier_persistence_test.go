package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convmem "github.com/viant/agently-core/app/store/data/memory"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	modelcallctx "github.com/viant/agently-core/service/core/modelcall"
)

type selectorLLMFinder struct {
	model llm.Model
}

func (f *selectorLLMFinder) Find(context.Context, string) (llm.Model, error) {
	return f.model, nil
}

type selectorLLMModel struct {
	content  string
	requests *[]*llm.GenerateRequest
}

func (m selectorLLMModel) Generate(ctx context.Context, request *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	if m.requests != nil {
		*m.requests = append(*m.requests, request)
	}
	var resp *llm.GenerateResponse
	if observer := modelcallctx.ObserverFromContext(ctx); observer != nil {
		startInfo := modelcallctx.Info{
			Provider:   "test",
			Model:      "router-model",
			ModelKind:  "chat",
			LLMRequest: request,
			StartedAt:  time.Now(),
		}
		var err error
		ctx, err = observer.OnCallStart(ctx, startInfo)
		if err != nil {
			return nil, err
		}
		defer func() {
			_ = observer.OnCallEnd(ctx, modelcallctx.Info{
				Model:       "router-model",
				ModelKind:   "chat",
				LLMRequest:  request,
				LLMResponse: resp,
				CompletedAt: time.Now(),
			})
		}()
	}
	resp = &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: m.content,
			},
		}},
	}
	return resp, nil
}

func (m selectorLLMModel) Implements(string) bool { return false }

type selectorPersistenceClient struct {
	apiconv.Client
	events         []string
	modelCallMsgID []string
	modelCallTurns []memory.TurnMeta
	modelCallErrs  []error
}

func (c *selectorPersistenceClient) PatchMessage(ctx context.Context, message *apiconv.MutableMessage) error {
	if message != nil {
		c.events = append(c.events, "message:"+message.Id)
	}
	return c.Client.PatchMessage(ctx, message)
}

func (c *selectorPersistenceClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	turn, _ := memory.TurnMetaFromContext(ctx)
	err := c.Client.PatchModelCall(ctx, modelCall)
	if modelCall != nil {
		c.events = append(c.events, "modelcall:"+modelCall.MessageID)
		c.modelCallMsgID = append(c.modelCallMsgID, modelCall.MessageID)
	}
	c.modelCallTurns = append(c.modelCallTurns, turn)
	c.modelCallErrs = append(c.modelCallErrs, err)
	return err
}

func TestEnsureAgent_AutoSelectionPersistsSelectorMessageAndModelCall(t *testing.T) {
	baseClient := convmem.New()
	client := &selectorPersistenceClient{Client: baseClient}

	conv := apiconv.NewConversation()
	conv.SetId("conv-1")
	conv.SetDefaultModel("router-model")
	require.NoError(t, client.PatchConversations(context.Background(), conv))

	llmSvc := core.New(&selectorLLMFinder{
		model: selectorLLMModel{content: `{"agentId":"coder"}`},
	}, nil, client)
	svc := &Service{
		llm:          llmSvc,
		conversation: client,
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Repository analysis, debugging, and code changes",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, and code changes",
					},
				},
				{
					Identity:    agentmdl.Identity{ID: "chatter", Name: "Chatter"},
					Description: "General conversation",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Chatter",
						Description: "General conversation and casual Q&A",
					},
				},
			},
		},
		defaults: &config.Defaults{},
	}

	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-1",
		AgentID:        "auto",
		UserId:         "user-1",
		Query:          "pick the best agent for this request",
	}

	require.NoError(t, svc.ensureAgent(context.Background(), input))
	require.NotNil(t, input.Agent)
	require.Equal(t, "coder", input.Agent.ID)

	require.NotEmpty(t, client.modelCallTurns)
	for _, turn := range client.modelCallTurns {
		require.Equal(t, "conv-1", turn.ConversationID)
		require.Equal(t, "turn-1", turn.TurnID)
	}

	require.NotEmpty(t, client.modelCallErrs)
	for _, err := range client.modelCallErrs {
		require.NoError(t, err)
	}

	require.NotEmpty(t, client.modelCallMsgID)
	msgID := client.modelCallMsgID[0]
	require.NotEmpty(t, msgID)
	for _, current := range client.modelCallMsgID {
		require.Equal(t, msgID, current)
	}

	msg, err := baseClient.GetMessage(context.Background(), msgID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, "conv-1", msg.ConversationId)
	require.Equal(t, "assistant", msg.Role)

	messageEvent := eventIndex(client.events, "message:"+msgID)
	modelCallEvent := eventIndex(client.events, "modelcall:"+msgID)
	require.NotEqual(t, -1, messageEvent)
	require.NotEqual(t, -1, modelCallEvent)
	require.Less(t, messageEvent, modelCallEvent)
}

func TestEnsureAgent_AutoSelectionIncludesRecentTranscript(t *testing.T) {
	ctx := context.Background()
	baseClient := convmem.New()
	client := &selectorPersistenceClient{Client: baseClient}

	conv := apiconv.NewConversation()
	conv.SetId("conv-1")
	conv.SetDefaultModel("router-model")
	require.NoError(t, client.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId("turn-prior")
	turn.SetConversationID("conv-1")
	turn.SetStatus("succeeded")
	turn.SetCreatedAt(time.Now())
	require.NoError(t, client.PatchTurn(ctx, turn))

	user := apiconv.NewMessage()
	user.SetId("msg-user-prior")
	user.SetConversationID("conv-1")
	user.SetTurnID("turn-prior")
	user.SetRole("user")
	user.SetType("text")
	user.SetContent("Troubleshoot order 2664518")
	user.SetCreatedAt(time.Now())
	require.NoError(t, client.PatchMessage(ctx, user))

	assistant := apiconv.NewMessage()
	assistant.SetId("msg-assistant-prior")
	assistant.SetConversationID("conv-1")
	assistant.SetTurnID("turn-prior")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("Order 2664518 is underdelivering; audience 7288336 is active.")
	assistant.SetCreatedAt(time.Now().Add(time.Second))
	require.NoError(t, client.PatchMessage(ctx, assistant))

	var requests []*llm.GenerateRequest
	llmSvc := core.New(&selectorLLMFinder{
		model: selectorLLMModel{content: `{"agentId":"coder"}`, requests: &requests},
	}, nil, client)
	svc := &Service{
		llm:          llmSvc,
		conversation: client,
		agentFinder: &allAgentFinder{
			items: []*agentmdl.Agent{
				{
					Identity:    agentmdl.Identity{ID: "coder", Name: "Coder"},
					Description: "Repository analysis, debugging, and code changes",
					Profile: &agentmdl.Profile{
						Publish:     true,
						Name:        "Coder",
						Description: "Repository analysis, debugging, and code changes",
					},
				},
			},
		},
		defaults: &config.Defaults{},
	}

	input := &QueryInput{
		ConversationID: "conv-1",
		MessageID:      "turn-current",
		AgentID:        "auto",
		UserId:         "user-1",
		Query:          "show me audience that deliver the most",
	}

	require.NoError(t, svc.ensureAgent(ctx, input))
	require.NotEmpty(t, requests)
	require.Len(t, requests[0].Messages, 2)
	userText := llm.MessageText(requests[0].Messages[1])
	require.Contains(t, userText, "Recent conversation context")
	require.Contains(t, userText, "Troubleshoot order 2664518")
	require.Contains(t, userText, "Order 2664518 is underdelivering")
	require.Contains(t, userText, "show me audience that deliver the most")
}

func TestRecentNonInterimTurnsTextSkipsNonFinalAssistantPhases(t *testing.T) {
	conv := &apiconv.Conversation{
		Transcript: []*agconv.TranscriptView{
			{
				Id: "turn-prior",
				Message: []*agconv.MessageView{
					{Role: "user", Type: "text", Content: strPtr("visible user")},
					{Role: "assistant", Type: "text", Phase: strPtr("intake"), Content: strPtr(`{"internal":"hidden"}`)},
					{Role: "assistant", Type: "text", Content: strPtr("visible assistant")},
				},
			},
		},
	}

	got := recentNonInterimTurnsText(conv, 3)

	require.Contains(t, got, "visible user")
	require.Contains(t, got, "visible assistant")
	require.NotContains(t, got, "hidden")
}

func eventIndex(events []string, target string) int {
	for i, event := range events {
		if event == target {
			return i
		}
	}
	return -1
}
