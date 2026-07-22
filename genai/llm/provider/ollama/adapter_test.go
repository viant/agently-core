package ollama

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/agently-core/genai/llm"
)

func TestToRequest_StreamFlag(t *testing.T) {
	testCases := []struct {
		description string
		input       llm.GenerateRequest
		expected    *Request
	}{
		{
			description: "streaming enabled",
			input: llm.GenerateRequest{
				Messages: []llm.Message{
					llm.NewUserMessage("Hello world"),
				},
				Options: &llm.Options{
					Temperature: 0.7,
					TopP:        0.8,
					MaxTokens:   5,
					Stream:      true,
				},
			},
			expected: &Request{
				Model:  "test-model",
				Stream: true,
				Format: "json",
				Options: &Options{
					Temperature:   0.7,
					TopP:          0.8,
					NumPredict:    5,
					RepeatPenalty: 1.1,
					Stop:          []string{"Human:", "User:"},
				},
				Prompt: "Human: Hello world\nAssistant: ",
			},
		},
		{
			description: "streaming disabled",
			input: llm.GenerateRequest{
				Messages: []llm.Message{
					llm.NewUserMessage("Test"),
				},
				Options: &llm.Options{Stream: false},
			},
			expected: &Request{
				Model:  "test-model",
				Stream: false,
				Format: "json",
				Options: &Options{
					Temperature:   0,
					TopP:          0,
					NumPredict:    0,
					RepeatPenalty: 1.1,
					Stop:          []string{"Human:", "User:"},
				},
				Prompt: "Human: Test\nAssistant: ",
			},
		},
		{
			description: "custom stop words",
			input: llm.GenerateRequest{
				Messages: []llm.Message{
					llm.NewUserMessage("Hi"),
				},
				Options: &llm.Options{
					Stream:    true,
					StopWords: []string{"Foo"},
				},
			},
			expected: &Request{
				Model:  "test-model",
				Stream: true,
				Format: "json",
				Options: &Options{
					Temperature:   0,
					TopP:          0,
					NumPredict:    0,
					RepeatPenalty: 1.1,
					Stop:          []string{"Foo"},
				},
				Prompt: "Human: Hi\nAssistant: ",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			actual, err := ToRequest(context.Background(), &tc.input, "test-model")
			if !assert.NoError(t, err) {
				return
			}
			assert.EqualValues(t, tc.expected, actual)
		})
	}
}

func TestClientPrepareRequest_AppliesModelDefaults(t *testing.T) {
	client, err := NewClient(context.Background(), "test-model", WithMaxTokens(128), WithKeepAlive("30m"), WithContextWindow(4096), WithTemperature(0.2))
	if !assert.NoError(t, err) {
		return
	}

	req, err := client.prepareRequest(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("Hello")},
	})
	if !assert.NoError(t, err) {
		return
	}
	if assert.NotNil(t, req.Options) {
		assert.Equal(t, 128, req.Options.NumPredict)
		assert.Equal(t, 4096, req.Options.NumCtx)
		assert.Equal(t, 0.2, req.Options.Temperature)
		assert.Equal(t, []string{"Human:", "User:"}, req.Options.Stop)
		assert.Equal(t, "30m", req.KeepAlive)
	}
}

func TestToRequest_UsesOutputSchemaAsOllamaFormat(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string"},
		},
		"required": []string{"action"},
	}
	req, err := ToRequest(context.Background(), &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("Return an action")},
		Options:  &llm.Options{OutputSchema: schema},
	}, "test-model")
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, schema, req.Format)
}
