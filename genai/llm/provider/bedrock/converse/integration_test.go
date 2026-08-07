//go:build integration

package converse

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
)

const qwen3CoderNext = "qwen.qwen3-coder-next"

func newIntegrationClient(t *testing.T) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), qwen3CoderNext,
		WithRegion("us-east-1"),
		WithCredentialsURL("aws-bedrock-qwen|blowfish://default"),
		WithMaxTokens(256),
	)
	require.NoError(t, err)
	return client
}

func TestIntegrationGenerateQwen3CoderNext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	response, err := newIntegrationClient(t).Generate(ctx, &llm.GenerateRequest{
		Messages: []llm.Message{llm.NewUserMessage("Reply with exactly: BEDROCK_OK")},
	})
	require.NoError(t, err)
	require.NotEmpty(t, response.Choices)
	require.Contains(t, response.Choices[0].Message.Content, "BEDROCK_OK")
	require.NotNil(t, response.Usage)
	require.Positive(t, response.Usage.TotalTokens)
}

func TestIntegrationStreamedFindToolCallQwen3CoderNext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := newIntegrationClient(t)
	userMessage := llm.NewUserMessage(`You must call the find tool once with query "bedrock converse". Do not answer from memory.`)
	tools := []llm.Tool{llm.NewFunctionTool(llm.ToolDefinition{
		Name: "find", Description: "Find an exact text query in the workspace",
		Parameters: map[string]interface{}{"query": map[string]interface{}{"type": "string"}}, Required: []string{"query"},
	})}
	stream, err := client.Stream(ctx, &llm.GenerateRequest{
		Messages: []llm.Message{userMessage},
		Options:  &llm.Options{Tools: tools},
	})
	require.NoError(t, err)
	var sawStart, sawDelta, sawCompleted, sawUsage, sawTurnCompleted bool
	var query string
	var toolCall llm.ToolCall
	for event := range stream {
		require.NoError(t, event.Err)
		switch event.Kind {
		case llm.StreamEventToolCallStarted:
			sawStart = event.ToolName == "find"
		case llm.StreamEventToolCallDelta:
			sawDelta = sawDelta || strings.TrimSpace(event.Delta) != ""
		case llm.StreamEventToolCallCompleted:
			sawCompleted = event.ToolName == "find"
			query, _ = event.Arguments["query"].(string)
			toolCall = llm.ToolCall{ID: event.ToolCallID, Name: event.ToolName, Arguments: event.Arguments, Type: "function"}
		case llm.StreamEventUsage:
			sawUsage = event.Usage != nil && event.Usage.TotalTokens > 0
		case llm.StreamEventTurnCompleted:
			sawTurnCompleted = true
		}
	}
	require.True(t, sawStart, "missing streamed tool-call start")
	require.True(t, sawDelta, "missing streamed tool-call arguments")
	require.True(t, sawCompleted, "missing streamed tool-call completion")
	require.Contains(t, strings.ToLower(query), "bedrock")
	require.True(t, sawUsage, "missing streamed usage")
	require.True(t, sawTurnCompleted, "missing turn completion")

	toolCall.Result = "BEDROCK_CONVERSE_RESULT_FOUND"
	response, err := client.Generate(ctx, &llm.GenerateRequest{
		Messages: []llm.Message{
			userMessage,
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{toolCall}},
			llm.NewToolResultMessage(toolCall),
		},
		Options: &llm.Options{Tools: tools},
	})
	require.NoError(t, err)
	require.NotEmpty(t, response.Choices)
	require.Contains(t, strings.ToLower(response.Choices[0].Message.Content), "found")
	require.Empty(t, response.Choices[0].Message.ToolCalls)
}
