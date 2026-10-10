package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/genai/llm"
	"github.com/viant/agently-core/protocol/binding"
	requestctx "github.com/viant/agently-core/runtime/requestctx"
)

func TestResourceBinaryForcesFullHistory(t *testing.T) {
	ctx := requestctx.WithTurnMeta(context.Background(), requestctx.TurnMeta{ConversationID: "conv"})
	h := &binding.History{LastResponse: &binding.Trace{ID: "response", At: time.Now()}, Traces: map[string]*binding.Trace{binding.KindToolCall.Key("call"): {ID: "response", Kind: binding.KindToolCall}}}
	for _, mime := range []string{"image/png", "application/pdf"} {
		req := &llm.GenerateRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "old message now includes new content", Items: []llm.ContentItem{llm.NewBinaryContent([]byte("data"), mime, "asset")}}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call", Name: "resources-read"}}}, {Role: llm.RoleTool, ToolCallId: "call", Content: "ready"}}}
		require.Nil(t, (&Service{}).BuildContinuationRequest(ctx, req, h), mime)
	}
	require.Nil(t, (&Service{}).BuildContinuationRequest(ctx, nil, nil))
}

func TestExpandedPromptKeepsNativeAttachments(t *testing.T) {
	a := &binding.Attachment{Name: "a.pdf", Mime: "application/pdf", Data: []byte("%PDF-x"), Native: true}
	history := &binding.History{CurrentTurnID: "turn", Current: &binding.Turn{ID: "turn", Messages: []*binding.Message{{ID: "user", Kind: binding.MessageKindChatUser, Role: "user", Content: "original", Attachment: []*binding.Attachment{a}}}}}
	messages := historyLLMMessagesWithExpandedCurrentPrompt(history, "expanded", []*binding.Attachment{a})
	require.Len(t, messages, 1)
	count := 0
	for _, item := range messages[0].Items {
		if item.Type == llm.ContentTypeBinary {
			count++
			require.Equal(t, true, item.Metadata["nativePresentation"])
		}
	}
	require.Equal(t, 1, count)
}

func TestNativePresentationPolicy(t *testing.T) {
	native := llm.NewBinaryContent([]byte("12345"), "application/pdf", "a.pdf")
	native.Metadata = map[string]interface{}{"nativePresentation": true}
	for _, tc := range []struct {
		name       string
		multimodal bool
		limit      int64
		wantError  bool
	}{{"unsupported", false, 100, true}, {"over limit", true, 4, true}, {"supported", true, 5, false}} {
		t.Run(tc.name, func(t *testing.T) {
			input := &GenerateInput{Binding: &binding.Binding{}, Message: []llm.Message{{Role: llm.RoleUser, Items: []llm.ContentItem{native}}}}
			input.Binding.Flags.IsMultimodal = tc.multimodal
			input.Options = &llm.Options{Metadata: map[string]interface{}{"nativePresentationLimitBytes": tc.limit}}
			svc := &Service{}
			err := svc.enforceAttachmentPolicy(context.Background(), input, nil)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NoError(t, svc.enforceAttachmentPolicy(context.Background(), input, nil))
				require.Len(t, input.Message[0].Items, 1)
			}
		})
	}
}

func TestCSVAttachmentSurvivesTextOnlyExpandedPrompt(t *testing.T) {
	a := &binding.Attachment{Name: "delivery.csv", Mime: "text/csv", Data: []byte("date,spend\n2026-09-09,300")}
	h := &binding.History{CurrentTurnID: "turn", Current: &binding.Turn{ID: "turn", Messages: []*binding.Message{{ID: "user", Kind: binding.MessageKindChatUser, Role: "user", Content: "analyze", Attachment: []*binding.Attachment{a}}}}}
	messages := historyLLMMessagesWithExpandedCurrentPrompt(h, "analyze", []*binding.Attachment{a})
	input := &GenerateInput{Binding: &binding.Binding{}, Message: messages}
	require.NoError(t, (&Service{}).enforceAttachmentPolicy(context.Background(), input, nil))
	require.Len(t, input.Message, 1)
	require.Equal(t, llm.ContentTypeText, input.Message[0].Items[0].Type)
	require.Contains(t, input.Message[0].Items[0].Text, "2026-09-09,300")
	original := h.Current.Messages[0].ToLLM()
	require.Equal(t, llm.ContentTypeText, original.Items[0].Type)
	require.Contains(t, original.Items[0].Text, "2026-09-09,300")
}

func TestExpandedPromptPreservesVerifiedUploadedResourceReference(t *testing.T) {
	attachment := &binding.Attachment{Name: "owned.xlsx", URI: "/v1/files/owned", ResourceURI: "scratchpad://artifact/owned", Data: []byte("owned bytes"), Mime: "application/octet-stream"}
	message := newExpandedUserLLMMessage("Export workbook", []*binding.Attachment{attachment}, "user")
	require.Contains(t, message.Content, "scratchpad://artifact/owned")
	require.NotContains(t, message.Content, "owned bytes")
	for _, item := range message.Items {
		require.Equal(t, llm.ContentTypeText, item.Type)
	}
	require.Equal(t, "user", message.ID)
}

func TestOrderedPromptPreservesVerifiedUploadedResourceReference(t *testing.T) {
	attachment := &binding.Attachment{Name: "book.xlsx", Mime: "application/octet-stream", ResourceURI: "scratchpad://artifact/workbook", Data: []byte("workbook bytes")}
	items := []llm.ContentItem{llm.NewTextContent("Use workbook"), llm.NewBinaryContent(attachment.Data, attachment.Mime, attachment.Name)}
	messages := historyLLMMessagesWithExpandedCurrentPrompt(nil, "Use workbook", []*binding.Attachment{attachment}, items)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, attachment.ResourceURI)
	require.Len(t, messages[0].Items, 2)
	require.Contains(t, messages[0].Items[1].Text, attachment.ResourceURI)
	historyMessage := (&binding.Message{Role: "user", Content: "Use workbook", Attachment: []*binding.Attachment{attachment}, ContentItems: items}).ToLLM()
	require.Len(t, historyMessage.Items, 2)
	require.Contains(t, historyMessage.Items[1].Text, attachment.ResourceURI)
}
