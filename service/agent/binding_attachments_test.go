package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	gfread "github.com/viant/agently-core/pkg/agently/generatedfile/read"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type stubConversationClient struct {
	payloads       map[string]*apiconv.Payload
	generatedFiles []*gfread.GeneratedFileView
	payloadWrites  []*apiconv.MutablePayload
	messageWrites  []*apiconv.MutableMessage
}

func (s *stubConversationClient) GetPayload(ctx context.Context, id string) (*apiconv.Payload, error) {
	if s.payloads == nil {
		return nil, nil
	}
	return s.payloads[id], nil
}

func (s *stubConversationClient) GetConversation(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Conversation, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubConversationClient) GetConversations(ctx context.Context, input *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubConversationClient) PatchConversations(ctx context.Context, conversations *apiconv.MutableConversation) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) PatchPayload(ctx context.Context, payload *apiconv.MutablePayload) error {
	s.payloadWrites = append(s.payloadWrites, payload)
	return nil
}
func (s *stubConversationClient) PatchMessage(ctx context.Context, message *apiconv.MutableMessage) error {
	s.messageWrites = append(s.messageWrites, message)
	return nil
}
func (s *stubConversationClient) GetMessage(ctx context.Context, id string, options ...apiconv.Option) (*apiconv.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubConversationClient) GetMessageByElicitation(ctx context.Context, conversationID, elicitationID string) (*apiconv.Message, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubConversationClient) PatchModelCall(ctx context.Context, modelCall *apiconv.MutableModelCall) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) PatchToolCall(ctx context.Context, toolCall *apiconv.MutableToolCall) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) PatchTurn(ctx context.Context, turn *apiconv.MutableTurn) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) DeleteConversation(ctx context.Context, id string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) DeleteMessage(ctx context.Context, conversationID, messageID string) error {
	return fmt.Errorf("not implemented")
}
func (s *stubConversationClient) GetGeneratedFiles(ctx context.Context, input *gfread.Input) ([]*gfread.GeneratedFileView, error) {
	var out []*gfread.GeneratedFileView
	for _, file := range s.generatedFiles {
		if file == nil {
			continue
		}
		if input != nil && input.Has != nil {
			if input.Has.ConversationID && file.ConversationID != input.ConversationID {
				continue
			}
			if input.Has.ID && file.ID != input.ID {
				continue
			}
		}
		out = append(out, file)
	}
	return out, nil
}
func (s *stubConversationClient) PatchGeneratedFile(ctx context.Context, generatedFile *apiconv.MutableGeneratedFile) error {
	return fmt.Errorf("not implemented")
}

func TestParseUploadedAttachmentURI(t *testing.T) {
	fileID, conversationID := parseUploadedAttachmentURI("/v1/files/file-1?conversationId=conv-1")
	assert.Equal(t, "file-1", fileID)
	assert.Equal(t, "conv-1", conversationID)

	fileID, conversationID = parseUploadedAttachmentURI("http://localhost:8080/v1/files/file-2?conversationId=conv-2")
	assert.Equal(t, "file-2", fileID)
	assert.Equal(t, "conv-2", conversationID)

	fileID, conversationID = parseUploadedAttachmentURI("https://example.com/image.png")
	assert.Empty(t, fileID)
	assert.Empty(t, conversationID)
}

func TestResolveUploadedAttachmentLoadsGeneratedFilePayload(t *testing.T) {
	payloadID := "payload-upload"
	payloadBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	svc := &Service{
		conversation: &stubConversationClient{
			payloads: map[string]*apiconv.Payload{
				payloadID: {
					Id:         payloadID,
					MimeType:   "image/png",
					InlineBody: &payloadBytes,
				},
			},
			generatedFiles: []*gfread.GeneratedFileView{{
				ID:             "file-upload",
				ConversationID: "conv-upload",
				PayloadID:      strPtr(payloadID),
				Filename:       strPtr("cat.png"),
				MimeType:       strPtr("image/png"),
			}},
		},
	}

	att := &binding.Attachment{
		URI: "/v1/files/file-upload?conversationId=conv-upload",
	}
	err := svc.resolveUploadedAttachment(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID: "conv-upload",
	}, att)
	require.NoError(t, err)
	assert.Equal(t, "cat.png", att.Name)
	assert.Equal(t, "image/png", att.Mime)
	assert.Equal(t, payloadBytes, att.Data)
	assert.Equal(t, payloadID, att.PayloadID)
}

func TestAddAttachmentReusesResolvedUploadPayload(t *testing.T) {
	payloadID := "payload-upload"
	client := &stubConversationClient{}
	svc := &Service{conversation: client}

	err := svc.addAttachment(context.Background(), runtimerequestctx.TurnMeta{
		ConversationID:  "conv-upload",
		TurnID:          "turn-upload",
		ParentMessageID: "msg-user",
	}, &binding.Attachment{
		Name:      "cat.png",
		URI:       "/v1/files/file-upload?conversationId=conv-upload",
		Mime:      "image/png",
		Data:      []byte{0x89, 0x50, 0x4e, 0x47},
		PayloadID: payloadID,
	})
	require.NoError(t, err)
	assert.Empty(t, client.payloadWrites)
	require.Len(t, client.messageWrites, 1)
	require.NotNil(t, client.messageWrites[0].AttachmentPayloadID)
	assert.Equal(t, payloadID, *client.messageWrites[0].AttachmentPayloadID)
}

func TestHistoryAttachmentCarriers_DataDriven(t *testing.T) {
	now := time.Now().UTC()
	payloadID := "payload-1"
	payloadBytes := []byte{0x01, 0x02, 0x03}

	type testCase struct {
		name           string
		carrierRole    string
		carrierFirst   bool
		expectMsgCount int
		expectAttName  string
		expectAttURI   string
		expectAttMIME  string
		expectAttBytes []byte
	}

	testCases := []testCase{
		{
			name:           "merges user control attachment",
			carrierRole:    "user",
			carrierFirst:   false,
			expectMsgCount: 1,
			expectAttName:  "img.png",
			expectAttURI:   "file:///tmp/img.png",
			expectAttMIME:  "image/png",
			expectAttBytes: payloadBytes,
		},
		{
			name:           "merges tool control attachment",
			carrierRole:    "tool",
			carrierFirst:   false,
			expectMsgCount: 1,
			expectAttName:  "img.png",
			expectAttURI:   "file:///tmp/img.png",
			expectAttMIME:  "image/png",
			expectAttBytes: payloadBytes,
		},
		{
			name:           "defers merge when carrier precedes parent",
			carrierRole:    "tool",
			carrierFirst:   true,
			expectMsgCount: 1,
			expectAttName:  "img.png",
			expectAttURI:   "file:///tmp/img.png",
			expectAttMIME:  "image/png",
			expectAttBytes: payloadBytes,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{
				conversation: &stubConversationClient{
					payloads: map[string]*apiconv.Payload{
						payloadID: {
							Id:         payloadID,
							MimeType:   "image/png",
							InlineBody: &payloadBytes,
							URI:        strPtr("file:///tmp/img.png"),
						},
					},
				},
			}

			parent := &apiconv.Message{
				Id:        "msg-user",
				Role:      "user",
				Type:      "text",
				Content:   strPtr("Task: analyze image"),
				CreatedAt: now,
			}
			carrier := &apiconv.Message{
				Id:                  "msg-att",
				Role:                tc.carrierRole,
				Type:                "control",
				Content:             strPtr("img.png"),
				CreatedAt:           now.Add(time.Millisecond),
				ParentMessageId:     strPtr(parent.Id),
				AttachmentPayloadId: strPtr(payloadID),
			}

			turnMsgs := []*agconv.MessageView{(*agconv.MessageView)(parent), (*agconv.MessageView)(carrier)}
			if tc.carrierFirst {
				turnMsgs = []*agconv.MessageView{(*agconv.MessageView)(carrier), (*agconv.MessageView)(parent)}
			}
			turn := &apiconv.Turn{
				Id:      "turn-1",
				Message: turnMsgs,
			}

			hist, err := svc.buildHistory(context.Background(), apiconv.Transcript{turn})
			require.NoError(t, err)
			require.Len(t, hist.Past, 1)
			require.Len(t, hist.Past[0].Messages, tc.expectMsgCount)

			got := hist.Past[0].Messages[0]
			require.NotNil(t, got)
			require.Len(t, got.Attachment, 1)
			assert.EqualValues(t, tc.expectAttName, got.Attachment[0].Name)
			assert.EqualValues(t, tc.expectAttURI, got.Attachment[0].URI)
			assert.EqualValues(t, tc.expectAttMIME, got.Attachment[0].Mime)
			assert.EqualValues(t, tc.expectAttBytes, got.Attachment[0].Data)
		})
	}
}

func TestHistoryAttachmentCarrierDoesNotDuplicateExpandedParentView(t *testing.T) {
	now := time.Now().UTC()
	payloadID := "payload-1"
	payloadBytes := []byte{0x25, 0x50, 0x44, 0x46}
	viewBytes := append([]byte(nil), payloadBytes...)
	payloadURI := "file:///tmp/story.pdf"

	svc := &Service{
		conversation: &stubConversationClient{
			payloads: map[string]*apiconv.Payload{
				payloadID: {
					Id:         payloadID,
					MimeType:   "application/pdf",
					InlineBody: &payloadBytes,
					URI:        strPtr(payloadURI),
				},
			},
		},
	}

	parent := &apiconv.Message{
		Id:        "msg-user",
		Role:      "user",
		Type:      "text",
		Content:   strPtr("what's in this file?"),
		CreatedAt: now,
		Attachment: []*agconv.AttachmentView{
			{
				InlineBody:      &viewBytes,
				MimeType:        "application/pdf",
				ParentMessageId: strPtr("msg-user"),
			},
		},
	}
	carrier := &apiconv.Message{
		Id:                  "msg-att",
		Role:                "user",
		Type:                "control",
		Content:             strPtr("story.pdf"),
		CreatedAt:           now.Add(time.Millisecond),
		ParentMessageId:     strPtr(parent.Id),
		AttachmentPayloadId: strPtr(payloadID),
	}
	turn := &apiconv.Turn{
		Id:      "turn-1",
		Message: []*agconv.MessageView{(*agconv.MessageView)(parent), (*agconv.MessageView)(carrier)},
	}

	hist, err := svc.buildHistory(context.Background(), apiconv.Transcript{turn})
	require.NoError(t, err)
	require.Len(t, hist.Past, 1)
	require.Len(t, hist.Past[0].Messages, 1)

	got := hist.Past[0].Messages[0]
	require.NotNil(t, got)
	require.Len(t, got.Attachment, 1)
	assert.Equal(t, "story.pdf", got.Attachment[0].Name)
	assert.Equal(t, payloadURI, got.Attachment[0].URI)
	assert.Equal(t, "application/pdf", got.Attachment[0].Mime)
	assert.Equal(t, payloadBytes, got.Attachment[0].Data)
}

func TestHistoryAttachmentViewOnlyParentStillWorks(t *testing.T) {
	now := time.Now().UTC()
	viewBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	parent := &apiconv.Message{
		Id:        "msg-user",
		Role:      "user",
		Type:      "text",
		Content:   strPtr("analyze this"),
		CreatedAt: now,
		Attachment: []*agconv.AttachmentView{
			{
				InlineBody:      &viewBytes,
				Uri:             strPtr("file:///tmp/view-only.png"),
				MimeType:        "image/png",
				ParentMessageId: strPtr("msg-user"),
			},
		},
	}
	turn := &apiconv.Turn{
		Id:      "turn-1",
		Message: []*agconv.MessageView{(*agconv.MessageView)(parent)},
	}

	hist, err := (&Service{}).buildHistory(context.Background(), apiconv.Transcript{turn})
	require.NoError(t, err)
	require.Len(t, hist.Past, 1)
	require.Len(t, hist.Past[0].Messages, 1)

	got := hist.Past[0].Messages[0]
	require.NotNil(t, got)
	require.Len(t, got.Attachment, 1)
	assert.Equal(t, "view-only.png", got.Attachment[0].Name)
	assert.Equal(t, "file:///tmp/view-only.png", got.Attachment[0].URI)
	assert.Equal(t, "image/png", got.Attachment[0].Mime)
	assert.Equal(t, viewBytes, got.Attachment[0].Data)
}

func TestHistoryMultipleAttachmentCarriersDoNotDuplicateExpandedParentView(t *testing.T) {
	now := time.Now().UTC()
	firstPayload := []byte{0x01}
	secondPayload := []byte{0x02}
	firstView := append([]byte(nil), firstPayload...)
	secondView := append([]byte(nil), secondPayload...)
	svc := &Service{
		conversation: &stubConversationClient{
			payloads: map[string]*apiconv.Payload{
				"payload-1": {
					Id:         "payload-1",
					MimeType:   "image/png",
					InlineBody: &firstPayload,
					URI:        strPtr("file:///tmp/one.png"),
				},
				"payload-2": {
					Id:         "payload-2",
					MimeType:   "image/png",
					InlineBody: &secondPayload,
					URI:        strPtr("file:///tmp/two.png"),
				},
			},
		},
	}

	parent := &apiconv.Message{
		Id:        "msg-user",
		Role:      "user",
		Type:      "text",
		Content:   strPtr("compare these"),
		CreatedAt: now,
		Attachment: []*agconv.AttachmentView{
			{InlineBody: &firstView, MimeType: "image/png", ParentMessageId: strPtr("msg-user")},
			{InlineBody: &secondView, MimeType: "image/png", ParentMessageId: strPtr("msg-user")},
		},
	}
	firstCarrier := &apiconv.Message{
		Id:                  "msg-att-1",
		Role:                "user",
		Type:                "control",
		Content:             strPtr("one.png"),
		CreatedAt:           now.Add(time.Millisecond),
		ParentMessageId:     strPtr(parent.Id),
		AttachmentPayloadId: strPtr("payload-1"),
	}
	secondCarrier := &apiconv.Message{
		Id:                  "msg-att-2",
		Role:                "user",
		Type:                "control",
		Content:             strPtr("two.png"),
		CreatedAt:           now.Add(2 * time.Millisecond),
		ParentMessageId:     strPtr(parent.Id),
		AttachmentPayloadId: strPtr("payload-2"),
	}
	turn := &apiconv.Turn{
		Id:      "turn-1",
		Message: []*agconv.MessageView{(*agconv.MessageView)(parent), (*agconv.MessageView)(firstCarrier), (*agconv.MessageView)(secondCarrier)},
	}

	hist, err := svc.buildHistory(context.Background(), apiconv.Transcript{turn})
	require.NoError(t, err)
	require.Len(t, hist.Past, 1)
	require.Len(t, hist.Past[0].Messages, 1)

	got := hist.Past[0].Messages[0]
	require.NotNil(t, got)
	require.Len(t, got.Attachment, 2)
	assert.Equal(t, "one.png", got.Attachment[0].Name)
	assert.Equal(t, "two.png", got.Attachment[1].Name)
}
