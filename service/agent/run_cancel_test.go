package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	"github.com/viant/agently-core/protocol/prompt"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

type canceledFinder struct{}

func (f *canceledFinder) Find(context.Context, string) (llm.Model, error) {
	return canceledModel{}, nil
}

type canceledModel struct{}

func (canceledModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return nil, context.Canceled
}

func (canceledModel) Implements(string) bool { return false }

func TestServiceRunPlanAndStatus_ReturnsCanceledWhenFirstLLMCallIsCanceled(t *testing.T) {
	t.Parallel()

	convClient := &dedupeConvClient{
		conversation: &apiconv.Conversation{
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
							Type:           "task",
							Content:        cancelPtr("hello"),
							TurnId:         cancelPtr("turn-1"),
						},
					},
				},
			},
		},
	}
	llmSvc := core.New(&canceledFinder{}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
	}
	input := &QueryInput{
		ConversationID: "conv-1",
		UserId:         "user-1",
		Query:          "hello",
		Agent: &agentmdl.Agent{
			Identity: agentmdl.Identity{ID: "simple"},
			ModelSelection: llm.ModelSelection{
				Model: "mock-model",
			},
			Prompt: &prompt.Prompt{Text: "You are helpful."},
		},
	}
	output := &QueryOutput{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})

	status, err := svc.runPlanAndStatus(ctx, input, output)
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "context canceled")
	require.Equal(t, "canceled", status)
}

func cancelPtr(value string) *string {
	return &value
}
