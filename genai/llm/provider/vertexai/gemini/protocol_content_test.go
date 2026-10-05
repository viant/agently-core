package gemini

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/runtime/clienttool"
	"testing"
)

func TestProtocolContentAllMediaSourcesAndOrderedToolParts(t *testing.T) {
	items, err := clienttool.MapContent(json.RawMessage(`[{"type":"text","text":"first"},{"type":"video","source":{"type":"file","value":"opaque:video","provider":"google","mimeType":"video/mp4"}},{"type":"audio","source":{"type":"data","value":"AQID","mimeType":"audio/wav"}},{"type":"image","source":{"type":"url","value":"https://example.test/image","mimeType":"image/png"}},{"type":"text","text":"last"}]`))
	require.NoError(t, err)
	request, err := ToRequest(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}, {Role: llm.RoleTool, ToolCallId: "call", Name: "lookup", Items: items}}})
	require.NoError(t, err)
	require.Len(t, request.Contents[0].Parts, 5)
	require.Equal(t, "first", request.Contents[0].Parts[0].Text)
	require.Equal(t, "opaque:video", request.Contents[0].Parts[1].FileData.FileURI)
	require.Equal(t, "AQID", request.Contents[0].Parts[2].InlineData.Data)
	require.Len(t, request.Contents[1].Parts, 6)
	require.NotNil(t, request.Contents[1].Parts[0].FunctionResponse)
	require.Equal(t, "last", request.Contents[1].Parts[5].Text)
	items[1].Provider = "openai"
	_, err = ToRequest(context.Background(), &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Items: items}}})
	require.ErrorContains(t, err, "cannot resolve")
}
