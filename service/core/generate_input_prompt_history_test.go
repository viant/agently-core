package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
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
			Task: binding.Task{Prompt: "Recommend sitelists for audience 7180287"},
			History: binding.History{
				CurrentTurnID: "turn-1",
				Current: &binding.Turn{
					ID: "turn-1",
					Messages: []*binding.Message{
						{
							Kind:    binding.MessageKindChatUser,
							Role:    string(llm.RoleUser),
							Content: "Recommend sitelists for audience 7180287",
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
	assert.Equal(t, "User Query:\nRecommend sitelists for audience 7180287\n\nEND_OF_USER_PROMPT", in.Message[0].Content)
	assert.Equal(t, "Recommend sitelists for audience 7180287", in.Binding.History.Current.Messages[0].Content)
}

func TestGenerateInputInit_ReplacesCurrentTurnPromptWhenCurrentTurnStillLivesInPast(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &binding.Prompt{Text: "User Query:\n{{.Task.Prompt}}\n\nEND_OF_USER_PROMPT", Engine: "go"},
		Binding: &binding.Binding{
			Task: binding.Task{Prompt: "Site-list recommendation workflow for audience 7180287 and matched target site_list_id 117385."},
			History: binding.History{
				CurrentTurnID: "turn-1",
				Past: []*binding.Turn{
					{
						ID: "turn-1",
						Messages: []*binding.Message{
							{
								Kind:    binding.MessageKindChatUser,
								Role:    string(llm.RoleUser),
								Content: "Site-list recommendation workflow for audience 7180287 and matched target site_list_id 117385.",
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
	assert.Equal(t, "User Query:\nSite-list recommendation workflow for audience 7180287 and matched target site_list_id 117385.\n\nEND_OF_USER_PROMPT", in.Message[0].Content)
	assert.Equal(t, llm.RoleAssistant, in.Message[1].Role)
	assert.Equal(t, "I have the IDs already.", in.Message[1].Content)
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
