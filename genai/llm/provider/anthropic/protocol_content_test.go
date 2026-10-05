package anthropic

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	vclaude "github.com/viant/agently-core/genai/llm/provider/vertexai/claude"
	"github.com/viant/agently-core/runtime/clienttool"
	"testing"
)

func TestProtocolContentDirectFilesAndOrderedToolMedia(t *testing.T) {
	items, err := clienttool.MapContent(json.RawMessage(`[{"type":"text","text":"first"},{"type":"image","source":{"type":"file","value":"file_opaque","provider":"anthropic","mimeType":"image/png"}},{"type":"document","source":{"type":"url","value":"https://example.test/document","mimeType":"application/pdf"}},{"type":"text","text":"last"}]`))
	require.NoError(t, err)
	request, err := ToRequest(context.Background(), "claude-test", &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}, {Role: llm.RoleTool, ToolCallId: "call", Items: items}}})
	require.NoError(t, err)
	require.Len(t, request.Messages[0].Content, 4)
	require.Equal(t, "file_opaque", request.Messages[0].Content[1].Source.FileID)
	parts := request.Messages[1].Content[0].Content.([]vclaude.ContentBlock)
	require.Len(t, parts, 4)
	require.Equal(t, "last", parts[3].Text)
	items[1].Provider = "google"
	_, err = ToRequest(context.Background(), "claude-test", &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}}})
	require.ErrorContains(t, err, "cannot resolve")
}
