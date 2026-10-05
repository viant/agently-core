package agent

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/genai/llm/provider/vertexai/gemini"
	authctx "github.com/viant/agently-core/internal/auth"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"github.com/viant/agently-core/runtime/clienttool"
	"github.com/viant/agently-core/service/shared/toolexec"
	"strings"
	"testing"
)

func TestAllMediaSourcesSurviveNativeDatlyRestartAndToolCompletion(t *testing.T) {
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	root := t.TempDir()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	conversation := apiconv.NewConversation()
	conversation.SetId("media-thread")
	conversation.SetCreatedByUserID("owner")
	conversation.SetStatus("active")
	require.NoError(t, conv.PatchConversations(ctx, conversation))
	turn := apiconv.NewTurn()
	turn.SetId("media-turn")
	turn.SetConversationID("media-thread")
	turn.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchTurn(ctx, turn))
	parts := []any{map[string]any{"type": "text", "text": "before", "metadata": map[string]any{"counter": json.Number("9007199254740993")}}}
	for _, kind := range []string{"image", "audio", "video", "document"} {
		for _, source := range []string{"url", "data", "file"} {
			parts = append(parts, map[string]any{"type": kind, "source": map[string]any{"type": source, "value": map[string]string{"url": "https://example.invalid/" + kind, "data": "AQID", "file": "opaque-" + kind}[source], "mimeType": map[string]string{"image": "image/png", "audio": "audio/wav", "video": "video/mp4", "document": "application/pdf"}[kind]}, "metadata": map[string]any{"kind": kind, "source": source}})
		}
	}
	for _, part := range parts {
		if p, ok := part.(map[string]any); ok {
			if source, ok := p["source"].(map[string]any); ok && source["type"] == "file" {
				source["provider"] = "google"
			}
		}
	}
	parts = append(parts, map[string]any{"type": "text", "text": "after"})
	raw, err := json.Marshal(parts)
	require.NoError(t, err)
	envelope, _ := json.Marshal(map[string]any{"threadId": "media-thread", "runId": "run", "messages": []any{map[string]any{"id": "media-user", "role": "user", "content": json.RawMessage(raw)}}})
	require.NoError(t, agui.ValidateInput(envelope))
	items, err := clienttool.MapContent(raw)
	require.NoError(t, err)
	storedItems, err := json.Marshal(items)
	require.NoError(t, err)
	user := apiconv.NewMessage()
	user.SetId("media-user")
	user.SetConversationID("media-thread")
	user.SetTurnID("media-turn")
	user.SetRole("user")
	user.SetType("task")
	user.SetRawContent(string(storedItems))
	marker := clienttool.ItemsMIME
	user.ContextSummary = &marker
	user.Has.ContextSummary = true
	require.NoError(t, conv.PatchMessage(ctx, user))
	assistant := apiconv.NewMessage()
	assistant.SetId("media-assistant")
	assistant.SetConversationID("media-thread")
	assistant.SetTurnID("media-turn")
	assistant.SetRole("assistant")
	assistant.SetType("text")
	assistant.SetContent("")
	require.NoError(t, conv.PatchMessage(ctx, assistant))
	tool := apiconv.NewMessage()
	tool.SetId("media-tool")
	tool.SetConversationID("media-thread")
	tool.SetTurnID("media-turn")
	tool.SetRole("tool")
	tool.SetType("tool_op")
	tool.SetStatus("waiting_for_user")
	tool.SetParentMessageID("media-assistant")
	require.NoError(t, conv.PatchMessage(ctx, tool))
	payload := apiconv.NewPayload()
	payload.SetId("media-request")
	payload.SetKind("tool_request")
	payload.SetMimeType("application/json")
	payload.SetStorage("inline")
	payload.SetInlineBody([]byte(`{}`))
	payload.SetSizeBytes(2)
	require.NoError(t, conv.PatchPayload(ctx, payload))
	call := apiconv.NewToolCall()
	call.SetMessageID("media-tool")
	call.SetTurnID("media-turn")
	call.SetOpID("media-call")
	call.SetToolName("browser")
	call.SetToolKind("function")
	call.SetStatus("waiting_for_user")
	request := "media-request"
	call.RequestPayloadID = &request
	call.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, call))
	pending := clienttool.PendingCall{ID: "media-call", Name: "browser", ToolMessageID: "media-tool", AssistantMessageID: "media-assistant", ConversationID: "media-thread", TurnID: "media-turn"}
	require.NoError(t, toolexec.CompleteClientToolMessage(ctx, conv, pending, raw))
	textTool := apiconv.NewMessage()
	textTool.SetId("gzip-text-tool")
	textTool.SetConversationID("media-thread")
	textTool.SetTurnID("media-turn")
	textTool.SetParentMessageID("media-assistant")
	textTool.SetRole("tool")
	textTool.SetType("tool_op")
	textTool.SetStatus("waiting_for_user")
	require.NoError(t, conv.PatchMessage(ctx, textTool))
	textCall := apiconv.NewToolCall()
	textCall.SetMessageID("gzip-text-tool")
	textCall.SetTurnID("media-turn")
	textCall.SetOpID("gzip-text-call")
	textCall.SetToolName("browser")
	textCall.SetToolKind("function")
	textCall.SetStatus("waiting_for_user")
	textCall.RequestPayloadID = &request
	textCall.Has.RequestPayloadID = true
	require.NoError(t, conv.PatchToolCall(ctx, textCall))
	textPending := pending
	textPending.ID = "gzip-text-call"
	textPending.ToolMessageID = "gzip-text-tool"
	textBody := strings.Repeat("exact gzip text receipt\n", 200)
	require.NoError(t, toolexec.CompleteClientTool(ctx, conv, textPending, textBody))
	require.NoError(t, server.Shutdown(ctx))
	restarted, err := native.New(ctx, native.Options{WorkspaceRoot: root})
	require.NoError(t, err)
	defer restarted.Shutdown(ctx)
	restored, err := convservice.New(ctx, restarted)
	require.NoError(t, err)
	saved, err := restored.GetConversation(ctx, "media-thread", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.NotNil(t, saved)
	for _, savedTurn := range saved.GetTranscript() {
		for _, message := range savedTurn.GetMessages() {
			if message.Id == "media-tool" {
				require.Equal(t, string(raw), message.GetContent(), "conversation graph media payload")
			} else if message.Id == textPending.ToolMessageID {
				require.Equal(t, textBody, message.GetContent(), "conversation graph gzip text payload")
			}
		}
	}
	history, err := (&Service{}).buildHistory(ctx, saved.GetTranscript())
	require.NoError(t, err)
	found := map[llm.MessageRole]bool{}
	for _, message := range history.LLMMessages() {
		if message.Role == llm.RoleTool && message.ToolCallId == textPending.ID {
			require.Equal(t, textBody, message.Content)
			continue
		}
		if message.Role == llm.RoleUser || message.Role == llm.RoleTool {
			require.Equal(t, items, message.Items, "ordered source handles/media/metadata survive restart")
			found[message.Role] = true
			if message.Role == llm.RoleTool {
				require.Equal(t, "media-call", message.ToolCallId)
			}
		}
	}
	require.True(t, found[llm.RoleUser])
	require.True(t, found[llm.RoleTool])
	requestWire, err := gemini.ToRequest(ctx, &llm.GenerateRequest{Messages: history.LLMMessages()})
	require.NoError(t, err)
	for _, content := range requestWire.Contents {
		if len(content.Parts) > 1 {
			parts := content.Parts
			if parts[0].FunctionResponse != nil {
				parts = parts[1:]
			}
			require.Len(t, parts, 14)
			require.Equal(t, "before", parts[0].Text)
			require.Equal(t, "after", parts[13].Text)
		}
	}
	receipt, err := restored.GetMessage(ctx, "media-tool", apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "gzip", receipt.MessageToolCall.MessageResponsePayload.Compression)
	require.Equal(t, string(raw), receipt.GetContent(), "stored receipt bytes")
	textReceipt, err := restored.GetMessage(ctx, textPending.ToolMessageID, apiconv.WithIncludeToolCall(true))
	require.NoError(t, err)
	require.Equal(t, "gzip", textReceipt.MessageToolCall.MessageResponsePayload.Compression)
	require.Equal(t, textBody, textReceipt.GetContent())
	require.NoError(t, toolexec.CompleteClientTool(ctx, restored, textPending, textBody))
	require.NoError(t, toolexec.CompleteClientToolMessage(ctx, restored, pending, raw), "identical receipt after restart is idempotent")
	require.Error(t, toolexec.CompleteClientToolMessage(ctx, restored, pending, json.RawMessage(`"different"`)), "conflicting receipt after restart cannot replace media")
}
