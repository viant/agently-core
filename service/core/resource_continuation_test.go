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
