package openai

import (
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"testing"
)

func TestSystemMessageCompatibilityIsOptInAndChatOnly(t *testing.T) {
	for _, tc := range []struct {
		enabled, responses bool
		count              int
	}{{false, false, 3}, {true, false, 2}, {true, true, 3}} {
		client := NewClient("synthetic", "example", WithContextContinuation(&tc.responses), WithMergeConsecutiveSystemMessages(tc.enabled))
		input := &llm.GenerateRequest{Messages: []llm.Message{llm.NewSystemMessage("First instruction"), llm.NewSystemMessage("Second instruction"), llm.NewUserMessage("Question")}}
		request, err := client.ToRequest(input)
		require.NoError(t, err)
		require.Len(t, request.Messages, tc.count)
		require.Len(t, input.Messages, 3)
		if tc.count == 2 {
			require.Equal(t, []ContentItem{{Type: "text", Text: "First instruction"}, {Type: "text", Text: "\n\n"}, {Type: "text", Text: "Second instruction"}}, request.Messages[0].Content)
		}
	}
}

func TestSystemMessageCompatibilityPreservesBoundariesAndParts(t *testing.T) {
	parts := []ContentItem{{Type: "text", Text: "Second"}, {Type: "image_url", ImageURL: &ImageURL{URL: "https://example.invalid/image"}}}
	input := []Message{{Role: "system", Content: "First"}, {Role: "system", Content: parts}, {Role: "user", Content: "Question"}, {Role: "system", Content: "Later"}, {Role: "system", Name: "named", Content: "Named"}, {Role: "tool", ToolCallId: "call-1", Content: "Result"}}
	out := mergeConsecutiveSystemMessages(input)
	require.Len(t, out, 5)
	require.Equal(t, input[2:], out[1:])
	require.Equal(t, []ContentItem{{Type: "text", Text: "First"}, {Type: "text", Text: "\n\n"}, parts[0], parts[1]}, out[0].Content)
	require.Equal(t, "First", input[0].Content)
	require.Len(t, parts, 2)
}
