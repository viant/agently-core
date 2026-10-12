package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	iauth "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/protocol/agui"
	mcpcfg "github.com/viant/agently-core/protocol/mcp/config"
	manager "github.com/viant/agently-core/protocol/mcp/manager"
	scratchpad "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"github.com/viant/agently-core/runtime/clienttool"
)

type browserArtifactConfig struct{ options *mcpcfg.MCPClient }

func (p browserArtifactConfig) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return p.options, nil
}
func (p browserArtifactConfig) Names(context.Context) ([]string, error) {
	return []string{"device"}, nil
}

func browserArtifactFixture(t *testing.T) (context.Context, *backendClient, clienttool.PendingCall, json.RawMessage, string, []byte) {
	t.Helper()
	t.Setenv(scratchpad.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	ctx := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "owner"})
	config := &mcpcfg.MCPClient{ExecutionLocation: "browser", OutputArtifacts: map[string]mcpcfg.OutputArtifact{"Capture": {Name: "screen.png"}}}
	mgr, err := manager.New(browserArtifactConfig{config})
	require.NoError(t, err)
	call := clienttool.PendingCall{ID: "call", ProtocolID: "protocol-call", Name: "device-Capture", ToolMessageID: "tool-message", ConversationID: "conversation", TurnID: "turn", Metadata: json.RawMessage(`{"browserMCP":{"catalogId":"catalog","server":"device","tool":"Capture","outputArtifact":true}}`)}
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	picture.Set(2, 3, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, picture))
	encoded := base64.StdEncoding.EncodeToString(b.Bytes())
	raw, err := json.Marshal(map[string]any{"content": []any{map[string]any{"type": "image", "data": encoded, "mimeType": "image/png"}}, "structuredContent": map[string]any{"duplicate": encoded}})
	require.NoError(t, err)
	content, err := json.Marshal(string(raw))
	require.NoError(t, err)
	return ctx, &backendClient{mcpMgr: mgr}, call, content, encoded, b.Bytes()
}

func TestBrowserOutputArtifactExtractsBeforeJournalAndReusesReference(t *testing.T) {
	ctx, client, call, content, encoded, pixels := browserArtifactFixture(t)
	makeInput := func() *agui.RunAgentInput {
		return &agui.RunAgentInput{ThreadID: "conversation", RunID: "reply", Messages: []agui.Message{{ID: call.ToolMessageID, Role: "tool", ToolCallID: aguiPendingCallID(call), Content: content}}}
	}
	first, second := makeInput(), makeInput()
	pending := aguiPending{ClientTools: []clienttool.PendingCall{call}}
	require.NoError(t, normalizeAGUIToolOutputs(ctx, client, first, pending))
	require.NoError(t, normalizeAGUIToolOutputs(ctx, client, second, pending))
	require.Equal(t, first.Messages[0].Content, second.Messages[0].Content, "lost reply must reuse artifact, never repeat tool")
	journal := rawAGUI(first)
	require.NotContains(t, string(journal), encoded)
	require.NotContains(t, string(journal), "duplicate")
	var text string
	require.NoError(t, json.Unmarshal(first.Messages[0].Content, &text))
	var result struct {
		Resources []struct {
			URI  string `json:"uri"`
			Mime string `json:"mimeType"`
		} `json:"resources"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &result))
	require.Len(t, result.Resources, 1)
	require.Equal(t, "image/png", result.Resources[0].Mime)
	stored, err := scratchpad.New().ReadArtifactPayload(ctx, result.Resources[0].URI)
	require.NoError(t, err)
	require.Equal(t, pixels, stored)
	foreign := iauth.WithUserInfo(context.Background(), &iauth.UserInfo{Subject: "other"})
	_, err = scratchpad.New().ReadArtifactPayload(foreign, result.Resources[0].URI)
	require.Error(t, err)
}

func TestBrowserOutputArtifactResumePreservesProvenanceAndDropsBinary(t *testing.T) {
	ctx, client, call, content, encoded, _ := browserArtifactFixture(t)
	payload := rawAGUI(map[string]any{"content": content})
	metadata := agui.WireMetadata{}
	require.NoError(t, json.Unmarshal(call.Metadata, &metadata))
	input := &agui.RunAgentInput{Resume: rawAGUI([]agui.WireResumeEntry{{InterruptId: aguiPendingCallID(call), Status: "resolved", Payload: &payload, Metadata: &metadata}})}
	require.NoError(t, normalizeAGUIToolOutputs(ctx, client, input, aguiPending{ClientTools: []clienttool.PendingCall{call}}))
	require.NotContains(t, string(rawAGUI(input)), encoded)
	var answers []agui.WireResumeEntry
	require.NoError(t, json.Unmarshal(input.Resume, &answers))
	output, toolError, err := aguiClientToolAnswer(call, answers[0])
	require.NoError(t, err)
	require.Empty(t, toolError)
	require.Contains(t, string(output), "scratchpad://artifact/")
}

func TestBrowserOutputArtifactFailureReturnsOneClosedToolError(t *testing.T) {
	ctx, client, call, _, _, _ := browserArtifactFixture(t)
	content := rawAGUI(`{"content":[{"type":"image","mimeType":"image/png","data":"PRIVATE_INVALID_DATA"}]}`)
	output, toolError, err := client.aguiNormalizeToolOutput(ctx, call, content, "")
	require.NoError(t, err)
	require.NotEmpty(t, toolError)
	require.NotContains(t, string(output), "PRIVATE_INVALID_DATA")
	require.Contains(t, toolError, "will not be replayed")
	call.Metadata = nil
	unchanged, _, err := client.aguiNormalizeToolOutput(ctx, call, content, "")
	require.NoError(t, err)
	require.Equal(t, content, unchanged, "ordinary client tools remain unchanged")
}

func TestBrowserOutputArtifactMetadataOnlyCaptureHasFiniteMissingBytesReason(t *testing.T) {
	ctx, client, call, _, _, _ := browserArtifactFixture(t)
	metadata := map[string]any{"artifact": map[string]any{"id": "native-capture", "mediaType": "image/png", "sizeBytes": 180000}, "metadata": map[string]any{"width": 1100, "height": 800}, "imageReturned": false, "inlineByteLimit": 96 * 1024}
	canonical := rawAGUI(map[string]any{"content": []any{map[string]any{"type": "text", "text": string(rawAGUI(metadata))}}, "structuredContent": metadata})
	output, toolError, err := client.aguiNormalizeToolOutput(ctx, call, rawAGUI(string(canonical)), "")
	require.NoError(t, err)
	require.Contains(t, toolError, "metadata without image or binary bytes")
	var text string
	require.NoError(t, json.Unmarshal(output, &text))
	var body struct {
		Resources  []any  `json:"resources"`
		ReasonCode string `json:"reasonCode"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &body))
	require.Empty(t, body.Resources)
	require.Equal(t, "outputArtifactBytesMissing", body.ReasonCode)
	items, _, err := scratchpad.New().ListArtifacts(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, items)
}
