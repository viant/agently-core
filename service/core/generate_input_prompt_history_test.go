package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestGenerateInputInit_AppendsLiveUserPromptAfterKnowledgeDocsWhenMissingFromHistory(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "hi"},
			Documents: binding.Documents{
				Items: []*binding.Document{
					{PageContent: "knowledge block"},
				},
			},
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 2)
	assert.Equal(t, llm.RoleUser, in.Message[0].Role)
	assert.Equal(t, "knowledge block", in.Message[0].Content)
	assert.Equal(t, llm.RoleUser, in.Message[1].Role)
	assert.Equal(t, "hi", in.Message[1].Content)
}

func TestGenerateInputInit_DoesNotDuplicateLiveUserPromptWhenAlreadyInCurrentHistory(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "hi"},
			Documents: binding.Documents{
				Items: []*binding.Document{
					{PageContent: "knowledge block"},
				},
			},
			History: binding.History{
				CurrentTurnID: "turn-1",
				Current: &binding.Turn{
					ID: "turn-1",
					Messages: []*binding.Message{
						{
							Kind:    binding.MessageKindChatUser,
							Role:    string(llm.RoleUser),
							Content: "hi",
						},
					},
				},
			},
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 2)
	assert.Equal(t, "knowledge block", in.Message[0].Content)
	assert.Equal(t, "hi", in.Message[1].Content)
}

func TestGenerateInputInit_ReplacesCurrentTurnDisplayTaskWithExpandedPromptForLLMOnly(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "User Query:\n{{.Task.Prompt}}\n\nEND_OF_USER_PROMPT", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "Recommend resource lists for workspace 7180287"},
			History: binding.History{
				CurrentTurnID: "turn-1",
				Current: &binding.Turn{
					ID: "turn-1",
					Messages: []*binding.Message{
						{
							ID:      "msg-current",
							Kind:    binding.MessageKindChatUser,
							Role:    string(llm.RoleUser),
							Content: "Recommend resource lists for workspace 7180287",
						},
					},
				},
			},
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 1)
	assert.Equal(t, llm.RoleUser, in.Message[0].Role)
	assert.Equal(t, "msg-current", in.Message[0].ID)
	assert.Equal(t, "User Query:\nRecommend resource lists for workspace 7180287\n\nEND_OF_USER_PROMPT", in.Message[0].Content)
	assert.Equal(t, "Recommend resource lists for workspace 7180287", in.Binding.History.Current.Messages[0].Content)
}

func TestGenerateInputInit_ReplacesCurrentTurnPromptWhenCurrentTurnStillLivesInPast(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "User Query:\n{{.Task.Prompt}}\n\nEND_OF_USER_PROMPT", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "resource-list recommendation workflow for workspace 7180287 and matched target resource_list_id 117385."},
			History: binding.History{
				CurrentTurnID: "turn-1",
				Past: []*binding.Turn{
					{
						ID: "turn-1",
						Messages: []*binding.Message{
							{
								ID:      "msg-current",
								Kind:    binding.MessageKindChatUser,
								Role:    string(llm.RoleUser),
								Content: "resource-list recommendation workflow for workspace 7180287 and matched target resource_list_id 117385.",
							},
							{
								Kind:    binding.MessageKindChatAssistant,
								Role:    string(llm.RoleAssistant),
								Content: "I have the IDs already.",
							},
						},
					},
				},
			},
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 2)
	assert.Equal(t, llm.RoleUser, in.Message[0].Role)
	assert.Equal(t, "msg-current", in.Message[0].ID)
	assert.Equal(t, "User Query:\nresource-list recommendation workflow for workspace 7180287 and matched target resource_list_id 117385.\n\nEND_OF_USER_PROMPT", in.Message[0].Content)
	assert.Equal(t, llm.RoleAssistant, in.Message[1].Role)
	assert.Equal(t, "I have the IDs already.", in.Message[1].Content)
}

func TestGenerateInputInit_PreservesCurrentUserMessageIDForContinuationReplay(t *testing.T) {
	baseTime := time.Now().UTC()
	history := binding.History{
		CurrentTurnID: "turn-current",
		Current: &binding.Turn{
			ID: "turn-current",
			Messages: []*binding.Message{
				{
					ID:        "msg-current-user",
					Kind:      binding.MessageKindChatUser,
					Role:      string(llm.RoleUser),
					Content:   "Open metric report builder",
					CreatedAt: baseTime.Add(time.Second),
				},
			},
		},
		Traces: map[string]*binding.Trace{
			binding.ContentMessageKey("msg-current-user"): {
				ID:   "",
				Kind: binding.KindContent,
				At:   baseTime.Add(time.Second),
			},
		},
		LastResponse: &binding.Trace{
			ID:   "resp-anchor",
			Kind: binding.KindResponse,
			At:   baseTime,
		},
	}

	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "Expanded:\n{{.Task.Prompt}}", Engine: "go"},
		Binding: &binding.Binding{
			Task:    binding.Task{Prompt: "Open metric report builder"},
			History: history,
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 1)
	assert.Equal(t, "msg-current-user", in.Message[0].ID)

	svc := &Service{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1"})
	req := &llm.GenerateRequest{Messages: append([]llm.Message(nil), in.Message...)}
	cont := svc.BuildContinuationRequest(ctx, req, &in.Binding.History)
	if assert.NotNil(t, cont) {
		require.Len(t, cont.Messages, 1)
		assert.Equal(t, llm.RoleUser, cont.Messages[0].Role)
		assert.Equal(t, "msg-current-user", cont.Messages[0].ID)
		assert.Equal(t, "Expanded:\nOpen metric report builder", cont.Messages[0].Content)
	}
}

func TestGenerateInputInit_PreservesMessageIDForReplayToolResults(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "continue"},
			History: binding.History{
				Past: []*binding.Turn{
					{
						ID: "turn-1",
						Messages: []*binding.Message{
							{
								ID:       "msg-assistant-1",
								Kind:     binding.MessageKindToolResult,
								Role:     string(llm.RoleAssistant),
								ToolOpID: "call_abc123",
								ToolName: "message-show",
								Content:  "{\"content\":\"payload\"}",
							},
						},
					},
				},
			},
		},
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 3)
	require.Equal(t, llm.RoleAssistant, in.Message[0].Role)
	require.Equal(t, "call_abc123", in.Message[0].ToolCalls[0].ID)
	require.Equal(t, llm.RoleTool, in.Message[1].Role)
	assert.Equal(t, "msg-assistant-1", in.Message[1].ID)
	assert.Equal(t, "call_abc123", in.Message[1].ToolCallId)
	require.Equal(t, llm.RoleUser, in.Message[2].Role)
	assert.Equal(t, "continue", in.Message[2].Content)
}
