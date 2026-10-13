package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/genai/llm"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/runtime/clienttool"
)

func TestResumeTurnQueryIgnoresGeneratedAttachmentCarriers(t *testing.T) {
	original, caption, payloadID, parentID := "Use the public fixture, select Green and apply once.", "Attached image", "image-payload", "original-user"
	imageOnly, err := json.Marshal([]llm.ContentItem{llm.NewBinaryContent([]byte("public synthetic image"), "image/png", "fixture.png")})
	require.NoError(t, err)
	imageAndText, err := json.Marshal([]llm.ContentItem{llm.NewTextContent(original), llm.NewBinaryContent([]byte("public synthetic image"), "image/png", "fixture.png")})
	require.NoError(t, err)
	mime, imageRaw, multipartRaw := clienttool.ItemsMIME, string(imageOnly), string(imageAndText)
	for _, test := range []struct {
		name     string
		original *conversationmodel.MessageView
		want     string
	}{
		{"text", &conversationmodel.MessageView{Id: parentID, Role: "user", Type: "text", Content: &original}, original},
		{"ordered multipart", &conversationmodel.MessageView{Id: parentID, Role: "user", Type: "text", ContextSummary: &mime, RawContent: &multipartRaw}, original},
		{"original image only", &conversationmodel.MessageView{Id: parentID, Role: "user", Type: "text", ContextSummary: &mime, RawContent: &imageRaw}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			turn := &apiconv.Turn{Message: []*conversationmodel.MessageView{test.original,
				{Id: "generated-image", Role: "user", Type: "control", Content: &caption, ParentMessageId: &parentID, AttachmentPayloadId: &payloadID},
			}}
			require.Equal(t, test.want, resumeTurnQuery(turn))
			require.Equal(t, caption, *turn.Message[1].Content, "intent selection must not mutate persisted carrier")
		})
	}
	followup := "Use Blue instead."
	turn := &apiconv.Turn{Message: []*conversationmodel.MessageView{
		{Role: "user", Type: "text", Content: &original},
		{Role: "user", Type: "text", Content: &followup},
		{Role: "user", Type: "control", Content: &caption, ParentMessageId: &parentID, AttachmentPayloadId: &payloadID},
	}}
	require.Equal(t, followup, resumeTurnQuery(turn), "a real user follow-up remains authoritative")
}

func TestResumeContinuationKeepsUserIntentAfterToolImageCarrier(t *testing.T) {
	svc, input, store, finder := continuationFixture(t)
	ctx := context.Background()
	original := "Use the public fixture, select Green and apply once."
	for _, test := range []struct {
		id, kind, content string
		carrier           bool
	}{
		{"original-user", "text", original, false}, {"generated-image", "control", "Attached image", true},
	} {
		message := apiconv.NewMessage()
		message.SetId(test.id)
		message.SetConversationID("resume-conv")
		message.SetTurnID("resume-turn")
		message.SetRole("user")
		message.SetType(test.kind)
		message.SetContent(test.content)
		message.SetCreatedAt(time.Now())
		if test.carrier {
			message.SetParentMessageID("original-user")
			message.SetAttachmentPayloadID("image-payload")
		}
		require.NoError(t, svc.conversation.PatchMessage(ctx, message))
	}
	payload := apiconv.NewPayload()
	payload.SetId("image-payload")
	payload.SetMimeType("image/png")
	payload.SetInlineBody([]byte("public synthetic image"))
	require.NoError(t, svc.conversation.PatchPayload(ctx, payload))
	turn := apiconv.NewTurn()
	turn.SetId("resume-turn")
	turn.SetConversationID("resume-conv")
	turn.SetStartedByMessageID("original-user")
	require.NoError(t, svc.conversation.PatchTurn(ctx, turn))
	output := &QueryOutput{}
	require.NoError(t, svc.ResumeContinuation(ctx, input, "resume-turn", 0, output))
	require.Equal(t, original, input.Query)
	require.Equal(t, original, input.DisplayQuery)
	require.Equal(t, "resume-turn", output.TurnID)
	require.EqualValues(t, 1, finder.calls.Load())
	require.Equal(t, 1, store.claims)
	conv, err := svc.conversation.GetConversation(ctx, "resume-conv", apiconv.WithIncludeTranscript(true))
	require.NoError(t, err)
	require.Len(t, conv.GetTranscript(), 1, "continuation must not admit another turn")
}
