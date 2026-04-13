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
		UserPromptAlreadyInHistory: false,
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
		UserPromptAlreadyInHistory: false,
	}

	err := in.Init(context.Background())
	require.NoError(t, err)
	require.Len(t, in.Message, 2)
	assert.Equal(t, "knowledge block", in.Message[0].Content)
	assert.Equal(t, "hi", in.Message[1].Content)
}
