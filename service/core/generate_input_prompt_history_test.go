package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/prompt"
)

func TestGenerateInputInit_AppendsLiveUserPromptAfterKnowledgeDocsWhenMissingFromHistory(t *testing.T) {
	in := &GenerateInput{
		ModelSelection: llm.ModelSelection{Model: "mock-model"},
		UserID:         "user-1",
		Prompt:         &prompt.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		Binding: &prompt.Binding{
			Task: prompt.Task{Prompt: "hi"},
			Documents: prompt.Documents{
				Items: []*prompt.Document{
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
		Prompt:         &prompt.Prompt{Text: "{{.Task.Prompt}}", Engine: "go"},
		Binding: &prompt.Binding{
			Task: prompt.Task{Prompt: "hi"},
			Documents: prompt.Documents{
				Items: []*prompt.Document{
					{PageContent: "knowledge block"},
				},
			},
			History: prompt.History{
				CurrentTurnID: "turn-1",
				Current: &prompt.Turn{
					ID: "turn-1",
					Messages: []*prompt.Message{
						{
							Kind:    prompt.MessageKindChatUser,
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
		Prompt:         &prompt.Prompt{Text: "User Query:\n{{.Task.Prompt}}\n\nEND_OF_USER_PROMPT", Engine: "go"},
		Binding: &prompt.Binding{
			Task: prompt.Task{Prompt: "Recommend sitelists for audience 7180287"},
			History: prompt.History{
				CurrentTurnID: "turn-1",
				Current: &prompt.Turn{
					ID: "turn-1",
					Messages: []*prompt.Message{
						{
							Kind:    prompt.MessageKindChatUser,
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
		Prompt:         &prompt.Prompt{Text: "User Query:\n{{.Task.Prompt}}\n\nEND_OF_USER_PROMPT", Engine: "go"},
		Binding: &prompt.Binding{
			Task: prompt.Task{Prompt: "Site-list recommendation workflow for audience 7180287 and matched target site_list_id 117385."},
			History: prompt.History{
				CurrentTurnID: "turn-1",
				Past: []*prompt.Turn{
					{
						ID: "turn-1",
						Messages: []*prompt.Message{
							{
								Kind:    prompt.MessageKindChatUser,
								Role:    string(llm.RoleUser),
								Content: "Site-list recommendation workflow for audience 7180287 and matched target site_list_id 117385.",
							},
							{
								Kind:    prompt.MessageKindChatAssistant,
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
