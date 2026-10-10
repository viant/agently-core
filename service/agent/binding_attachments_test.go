package agent

import (
	"bytes"
	"context"
	"fmt"
	authctx "github.com/viant/agently-core/internal/auth"
	scratchpadsvc "github.com/viant/agently-core/protocol/tool/service/scratchpad"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	generatedfilemodel "github.com/viant/agently-core/model/generatedfile"
	"github.com/viant/agently-core/protocol/binding"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type stubConversationClient struct {
	payloads       map[string]*apiconv.Payload
	generatedFiles []*generatedfilemodel.GeneratedFileView
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
func (s *stubConversationClient) GetGeneratedFiles(ctx context.Context, input *generatedfilemodel.Input) ([]*generatedfilemodel.GeneratedFileView, error) {
	var out []*generatedfilemodel.GeneratedFileView
	for _, file := range s.generatedFiles {
		if file == nil {
			continue
		}
		if input != nil && input.Has != nil {
			if input.Has.ConversationID && file.ConversationId != input.ConversationID {
				continue
			}
			if input.Has.ID && file.Id != input.ID {
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
			generatedFiles: []*generatedfilemodel.GeneratedFileView{{
				Id:             "file-upload",
				ConversationId: "conv-upload",
				PayloadId:      strPtr(payloadID),
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

			turnMsgs := []*conversationmodel.MessageView{(*conversationmodel.MessageView)(parent), (*conversationmodel.MessageView)(carrier)}
			if tc.carrierFirst {
				turnMsgs = []*conversationmodel.MessageView{(*conversationmodel.MessageView)(carrier), (*conversationmodel.MessageView)(parent)}
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
		Attachment: []*conversationmodel.AttachmentView{
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
		Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(parent), (*conversationmodel.MessageView)(carrier)},
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
		Attachment: []*conversationmodel.AttachmentView{
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
		Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(parent)},
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
		Attachment: []*conversationmodel.AttachmentView{
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
		Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(parent), (*conversationmodel.MessageView)(firstCarrier), (*conversationmodel.MessageView)(secondCarrier)},
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

func TestAttachmentResourceReferenceRechecksCurrentOwner(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	descriptor, err := scratchpadsvc.New().PublishArtifact(owner, "owned", "owned.xlsx", "application/octet-stream", "", bytes.NewReader([]byte("owned bytes")))
	require.NoError(t, err)
	attachment := &binding.Attachment{Name: "owned.xlsx", URI: "/v1/files/owned"}
	bindAttachmentResourceReference(owner, attachment, descriptor.URI)
	require.Equal(t, descriptor.URI, attachment.ResourceURI)
	bindAttachmentResourceReference(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"}), attachment, descriptor.URI)
	require.Empty(t, attachment.ResourceURI)
	bindAttachmentResourceReference(context.Background(), attachment, descriptor.URI)
	require.Empty(t, attachment.ResourceURI)
	bindAttachmentResourceReference(owner, nil, descriptor.URI)
}

func TestUploadedArtifactReferencesInCurrentAndHistory(t *testing.T) {
	t.Setenv(scratchpadsvc.EnvScratchpadURI, "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "${userID}")))
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	body := []byte("workbook fixture")
	descriptor, err := scratchpadsvc.New().PublishArtifact(owner, "workbook", "book.xlsx", "application/octet-stream", "", bytes.NewReader(body))
	require.NoError(t, err)
	client := &stubConversationClient{
		payloads:       map[string]*apiconv.Payload{"payload": {Id: "payload", URI: strPtr(descriptor.URI), InlineBody: &body, MimeType: descriptor.MimeType}},
		generatedFiles: []*generatedfilemodel.GeneratedFileView{{Id: "file", ConversationId: "conv", PayloadId: strPtr("payload"), Filename: strPtr("book.xlsx")}},
	}
	svc := &Service{conversation: client}
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		verified bool
	}{
		{"owner", owner, true},
		{"foreign", authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"}), false},
		{"anonymous", context.Background(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attachment := &binding.Attachment{URI: "/v1/files/file?conversationId=conv"}
			require.NoError(t, svc.resolveUploadedAttachment(tc.ctx, runtimerequestctx.TurnMeta{ConversationID: "conv"}, attachment))
			current := (&binding.Message{Role: "user", Content: "Use workbook", Attachment: []*binding.Attachment{attachment}}).ToLLM()
			require.Equal(t, tc.verified, strings.Contains(current.Content, descriptor.URI))
			require.NotContains(t, current.Content, string(body))
			require.Equal(t, tc.verified, attachment.ResourceMetadataOnly())
			for _, item := range current.Items {
				if tc.verified {
					require.Equal(t, "text", string(item.Type))
				}
			}
			require.Equal(t, body, attachment.Data)
			parent := &apiconv.Message{Id: "user", Role: "user", Type: "text", Content: strPtr("Use workbook"), CreatedAt: time.Now()}
			carrier := &apiconv.Message{Id: "attachment", Role: "user", Type: "control", Content: strPtr("book.xlsx"), ParentMessageId: strPtr(parent.Id), AttachmentPayloadId: strPtr("payload"), CreatedAt: parent.CreatedAt.Add(time.Millisecond)}
			history, err := svc.buildHistory(tc.ctx, apiconv.Transcript{&apiconv.Turn{Id: "turn", Message: []*conversationmodel.MessageView{(*conversationmodel.MessageView)(parent), (*conversationmodel.MessageView)(carrier)}}})
			require.NoError(t, err)
			messages := history.LLMMessages()
			require.Len(t, messages, 1)
			require.Equal(t, tc.verified, strings.Contains(messages[0].Content, descriptor.URI))
			require.NotContains(t, messages[0].Content, string(body))
			for _, item := range messages[0].Items {
				if tc.verified {
					require.Equal(t, "text", string(item.Type))
				}
			}
			view := &apiconv.Message{Attachment: []*conversationmodel.AttachmentView{{Uri: strPtr(descriptor.URI), InlineBody: &body, MimeType: descriptor.MimeType}}}
			views, err := svc.attachmentsFromMessage(tc.ctx, view, nil, true)
			require.NoError(t, err)
			require.Len(t, views, 1)
			require.Equal(t, tc.verified, views[0].ResourceURI != "")
		})
	}
	// A verified reference can survive a metadata-only payload without opening its bytes.
	cache := map[string]*binding.Attachment{}
	message := &apiconv.Message{AttachmentPayloadId: strPtr("payload")}
	_, err = svc.attachmentsFromMessage(owner, message, cache, false)
	require.NoError(t, err)
	foreign := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"})
	cached, err := svc.attachmentsFromMessage(foreign, message, cache, false)
	require.NoError(t, err)
	require.Empty(t, cached[0].ResourceURI, "cached payloads must recheck owner")
	require.False(t, cached[0].ResourceMetadataOnly())
	require.Equal(t, body, cached[0].Data)
	ownerModel := (&binding.Message{Role: "user", Attachment: []*binding.Attachment{cache["payload"]}}).ToLLM()
	for _, item := range ownerModel.Items {
		require.Equal(t, "text", string(item.Type))
	}
	foreignModel := (&binding.Message{Role: "user", Attachment: cached}).ToLLM()
	require.Equal(t, "binary", string(foreignModel.Items[0].Type), "unverified attachments keep their normal presentation")
	require.Equal(t, descriptor.URI, cache["payload"].ResourceURI)
	client.payloads["payload"].InlineBody = nil
	attachments, err := svc.attachmentsFromMessage(owner, &apiconv.Message{AttachmentPayloadId: strPtr("payload")}, nil, false)
	require.NoError(t, err)
	require.Len(t, attachments, 1)
	require.Empty(t, attachments[0].Data)
	require.Equal(t, descriptor.URI, attachments[0].ResourceURI)
	_, err = svc.attachmentsFromMessage(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "foreign"}), &apiconv.Message{AttachmentPayloadId: strPtr("payload")}, nil, false)
	require.Error(t, err)
}
