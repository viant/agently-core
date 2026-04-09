package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor/config"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agentmdl "github.com/viant/agently-core/protocol/agent"
	asynccfg "github.com/viant/agently-core/protocol/async"
	"github.com/viant/agently-core/protocol/prompt"
	memory "github.com/viant/agently-core/runtime/requestctx"
	"github.com/viant/agently-core/service/core"
	"github.com/viant/agently-core/service/reactor"
)

func TestInjectAsyncReinforcement_AddsSystemMessage(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "llm/agents:run",
		WaitForResponse: true,
		Status:          "running",
		Message:         "still working",
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.Equal(t, "system", client.lastMessage.Role)
	require.NotNil(t, client.lastMessage.Mode)
	require.Equal(t, "async_wait", *client.lastMessage.Mode)
	require.NotNil(t, client.lastMessage.Content)
	require.Contains(t, *client.lastMessage.Content, "op-1")
	require.Contains(t, *client.lastMessage.Content, "still in progress")
}

func TestInjectAsyncReinforcement_ConsumesPendingChange(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
	})

	svc.injectAsyncReinforcement(ctx, turn)
	changed := svc.asyncManager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changed, 0)
}

func TestInjectAsyncReinforcement_RendersConfiguredPromptFromFile(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	dir := t.TempDir()
	promptPath := filepath.Join(dir, "async.tmpl")
	err := os.WriteFile(promptPath, []byte("ASYNC {{.Context.operation.id}} {{.Context.operation.status}}"), 0o644)
	require.NoError(t, err)

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
		Reinforcement: &asynccfg.PromptConfig{
			URI:    promptPath,
			Engine: "go",
		},
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Equal(t, "ASYNC op-1 running", strings.TrimSpace(*client.lastMessage.Content))
}

func TestInjectAsyncReinforcement_RendersReinforcementPromptTemplate(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                  "op-1",
		ParentConvID:        "conv-1",
		ParentTurnID:        "turn-1",
		ToolName:            "forecasting-Total",
		WaitForResponse:     true,
		Status:              "WAITING",
		RequestArgsDigest:   `{"DealsPmpIncl":[142133]}`,
		RequestArgs:         map[string]interface{}{"DealsPmpIncl": []int{142133}},
		ReinforcementPrompt: "status={{.Context.operation.status}} args={{.Context.operation.requestArgsJSON}}",
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Equal(t, `status=WAITING args={"DealsPmpIncl":[142133]}`, strings.TrimSpace(*client.lastMessage.Content))
}

func TestInjectAsyncReinforcement_TerminalPromptTellsModelToAnswer(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                "op-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting-Total",
		WaitForResponse:   true,
		Status:            "COMPLETE",
		KeyData:           []byte(`[{"inventory":1}]`),
		RequestArgsDigest: `{"DealsPmpIncl":[142130]}`,
		RequestArgs:       map[string]interface{}{"DealsPmpIncl": []int{142130}},
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	content := strings.TrimSpace(*client.lastMessage.Content)
	require.Contains(t, content, "Do not call the async tool again")
	require.Contains(t, content, "Answer the user")
}

func TestInjectAsyncReinforcement_UsesConfiguredPromptForTerminalState(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                  "op-1",
		ParentConvID:        "conv-1",
		ParentTurnID:        "turn-1",
		ToolName:            "forecasting-Total",
		WaitForResponse:     true,
		Status:              "COMPLETE",
		ReinforcementPrompt: `{{- if eq .Context.operation.state "completed" -}}answer-now{{- else -}}poll-again{{- end -}}`,
	})

	svc.injectAsyncReinforcement(ctx, turn)

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Equal(t, "answer-now", strings.TrimSpace(*client.lastMessage.Content))
}

func TestInjectAsyncReinforcement_ProvidesTurnAsyncAggregateContext(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                  "op-complete",
		ParentConvID:        "conv-1",
		ParentTurnID:        "turn-1",
		ToolName:            "forecasting-Total",
		WaitForResponse:     true,
		Status:              "COMPLETE",
		RequestArgsDigest:   `{"From":"2026-04-09T00:00:00Z"}`,
		RequestArgs:         map[string]interface{}{"From": "2026-04-09T00:00:00Z"},
		ReinforcementPrompt: `op={{.Context.operation.state}} turnPending={{.Context.turnAsync.pending}} allResolved={{.Context.turnAsync.allResolved}}`,
	})
	_, _ = svc.asyncManager.Update(ctx, asynccfg.UpdateInput{
		ID:     "op-complete",
		Status: "COMPLETE",
		State:  asynccfg.StateCompleted,
	})
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                "op-waiting",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting-Total",
		WaitForResponse:   true,
		Status:            "WAITING",
		RequestArgsDigest: `{"From":"2026-04-10T00:00:00Z"}`,
		RequestArgs:       map[string]interface{}{"From": "2026-04-10T00:00:00Z"},
	})

	svc.injectAsyncReinforcementForRecords(ctx, turn, []*asynccfg.OperationRecord{
		{
			ID:                  "op-complete",
			ParentConvID:        "conv-1",
			ParentTurnID:        "turn-1",
			ToolName:            "forecasting-Total",
			WaitForResponse:     true,
			State:               asynccfg.StateCompleted,
			Status:              "COMPLETE",
			RequestArgsDigest:   `{"From":"2026-04-09T00:00:00Z"}`,
			RequestArgs:         map[string]interface{}{"From": "2026-04-09T00:00:00Z"},
			ReinforcementPrompt: `op={{.Context.operation.state}} turnPending={{.Context.turnAsync.pending}} allResolved={{.Context.turnAsync.allResolved}}`,
		},
	})

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Equal(t, "op=completed turnPending=1 allResolved=false", strings.TrimSpace(*client.lastMessage.Content))
}

func TestInjectAsyncReinforcement_UsesLowercaseAggregateFieldsInPrompt(t *testing.T) {
	ctx := context.Background()
	client := &recordingConvClient{}
	svc := &Service{
		conversation: client,
		asyncManager: asynccfg.NewManager(),
	}

	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                  "op-complete",
		ParentConvID:        "conv-1",
		ParentTurnID:        "turn-1",
		ToolName:            "forecasting-Total",
		WaitForResponse:     true,
		Status:              "COMPLETE",
		RequestArgsDigest:   `{"From":"2026-04-09T00:00:00Z"}`,
		RequestArgs:         map[string]interface{}{"From": "2026-04-09T00:00:00Z"},
		ReinforcementPrompt: `pending={{.Context.operation.turnpending}} resolved={{.Context.operation.turnallresolved}}`,
	})
	_, _ = svc.asyncManager.Update(ctx, asynccfg.UpdateInput{
		ID:     "op-complete",
		Status: "COMPLETE",
		State:  asynccfg.StateCompleted,
	})
	svc.asyncManager.Register(ctx, asynccfg.RegisterInput{
		ID:                "op-waiting",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting-Total",
		WaitForResponse:   true,
		Status:            "WAITING",
		RequestArgsDigest: `{"From":"2026-04-10T00:00:00Z"}`,
		RequestArgs:       map[string]interface{}{"From": "2026-04-10T00:00:00Z"},
	})

	svc.injectAsyncReinforcementForRecords(ctx, turn, []*asynccfg.OperationRecord{
		{
			ID:                  "op-complete",
			ParentConvID:        "conv-1",
			ParentTurnID:        "turn-1",
			ToolName:            "forecasting-Total",
			WaitForResponse:     true,
			State:               asynccfg.StateCompleted,
			Status:              "COMPLETE",
			RequestArgsDigest:   `{"From":"2026-04-09T00:00:00Z"}`,
			RequestArgs:         map[string]interface{}{"From": "2026-04-09T00:00:00Z"},
			ReinforcementPrompt: `pending={{.Context.operation.turnpending}} resolved={{.Context.operation.turnallresolved}}`,
		},
	})

	require.NotNil(t, client.lastMessage)
	require.NotNil(t, client.lastMessage.Content)
	require.Equal(t, "pending=1 resolved=false", strings.TrimSpace(*client.lastMessage.Content))
}

func TestMarkAssistantMessageInterim_PatchesLatestAssistantMessage(t *testing.T) {
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	ctx = context.WithValue(ctx, memory.ModelMessageIDKey, "assistant-1")

	client := &recordingConvClient{}
	svc := &Service{conversation: client}
	turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	svc.markAssistantMessageInterim(ctx, turn, &core.GenerateOutput{MessageID: "assistant-1"})

	require.NotNil(t, client.lastMessage)
	require.Equal(t, "assistant-1", client.lastMessage.Id)
	require.Equal(t, "conv-1", client.lastMessage.ConversationID)
	require.NotNil(t, client.lastMessage.Interim)
	require.Equal(t, 1, *client.lastMessage.Interim)
}

type terminalAsyncFinder struct {
	content string
}

func (f *terminalAsyncFinder) Find(context.Context, string) (llm.Model, error) {
	return terminalAsyncModel{content: f.content}, nil
}

type terminalAsyncModel struct {
	content string
}

func (m terminalAsyncModel) Generate(context.Context, *llm.GenerateRequest) (*llm.GenerateResponse, error) {
	return &llm.GenerateResponse{
		Choices: []llm.Choice{{
			Index:   0,
			Message: llm.NewAssistantMessage(m.content),
		}},
	}, nil
}

func (m terminalAsyncModel) Implements(string) bool { return false }

func TestServiceRunPlanAndStatus_AllowsModelFinalAnswerAfterTerminalAsyncState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		state        asynccfg.State
		status       string
		finalContent string
	}{
		{
			name:         "failed async op does not abort",
			state:        asynccfg.StateFailed,
			status:       "failed",
			finalContent: "ASYNC_FAIL_DONE status=failed",
		},
		{
			name:         "canceled async op does not abort",
			state:        asynccfg.StateCanceled,
			status:       "canceled",
			finalContent: "ASYNC_CANCEL_DONE status=canceled",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
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

			llmSvc := core.New(&terminalAsyncFinder{content: tc.finalContent}, nil, convClient)
			svc := &Service{
				llm:          llmSvc,
				conversation: convClient,
				orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
				defaults:     &config.Defaults{},
				asyncManager: asynccfg.NewManager(),
			}

			svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
				ID:              "op-1",
				ParentConvID:    "conv-1",
				ParentTurnID:    "turn-1",
				ToolName:        "system/exec:start",
				WaitForResponse: true,
				Status:          tc.status,
				Error: func() string {
					if tc.state == asynccfg.StateFailed {
						return "boom"
					}
					return ""
				}(),
			})
			_, _ = svc.asyncManager.Update(context.Background(), asynccfg.UpdateInput{
				ID:     "op-1",
				Status: tc.status,
				Error: func() string {
					if tc.state == asynccfg.StateFailed {
						return "boom"
					}
					return ""
				}(),
				State: tc.state,
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
					Prompt: &prompt.Prompt{Text: "You are helpful."},
				},
			}
			output := &QueryOutput{}
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
				ConversationID: "conv-1",
				TurnID:         "turn-1",
			})
			ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1"})

			status, err := svc.runPlanAndStatus(ctx, input, output)
			require.NoError(t, err)
			require.Equal(t, "succeeded", status)
			require.NotNil(t, output)
			require.Equal(t, tc.finalContent, strings.TrimSpace(output.Content))
		})
	}
}

func TestServiceRunPlanAndStatus_DoesNotFinalizeWhileAnyAsyncWaitOpRemains(t *testing.T) {
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

	llmSvc := core.New(&terminalAsyncFinder{content: "SHOULD_NOT_FINALIZE"}, nil, convClient)
	svc := &Service{
		llm:          llmSvc,
		conversation: convClient,
		orchestrator: reactor.New(llmSvc, nil, convClient, nil, nil),
		defaults:     &config.Defaults{},
		asyncManager: asynccfg.NewManager(),
	}

	svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
		ID:              "op-complete",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "forecasting-Total",
		WaitForResponse: true,
		Status:          "COMPLETE",
	})
	_, _ = svc.asyncManager.Update(context.Background(), asynccfg.UpdateInput{
		ID:     "op-complete",
		Status: "COMPLETE",
		State:  asynccfg.StateCompleted,
	})

	svc.asyncManager.Register(context.Background(), asynccfg.RegisterInput{
		ID:              "op-waiting",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "forecasting-Total",
		WaitForResponse: true,
		Status:          "WAITING",
		PollIntervalMs:  500,
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
			Prompt: &prompt.Prompt{Text: "You are helpful."},
		},
	}
	output := &QueryOutput{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	ctx = memory.WithRunMeta(ctx, memory.RunMeta{RunID: "turn-1"})
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	status, err := svc.runPlanAndStatus(ctx, input, output)
	require.Error(t, err)
	require.Equal(t, "canceled", status)
	require.NotEqual(t, "SHOULD_NOT_FINALIZE", strings.TrimSpace(output.Content))
}
