package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/viant/afs"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/protocol/prompt"
)

func cloneMessageWithoutToolMessages(msg *apiconv.Message) *apiconv.Message {
	if msg == nil {
		return nil
	}
	if len(msg.ToolMessage) == 0 {
		return msg
	}
	clone := *msg
	clone.ToolMessage = nil
	return &clone
}

func (s *Service) syntheticToolMessages(ctx context.Context, msg *apiconv.Message) []*apiconv.Message {
	if msg == nil || len(msg.ToolMessage) == 0 {
		return nil
	}
	out := make([]*apiconv.Message, 0, len(msg.ToolMessage))
	for _, tm := range msg.ToolMessage {
		if tm == nil || tm.ToolCall == nil {
			continue
		}
		body := s.toolMessageResponseBody(ctx, tm)
		if body == "" {
			continue
		}
		toolRole := "assistant"
		toolType := "tool_op"
		turnID := msg.TurnId
		parentID := msg.ParentMessageId
		createdBy := msg.CreatedByUserId
		out = append(out, &apiconv.Message{
			Id:              tm.Id,
			ConversationId:  msg.ConversationId,
			TurnId:          turnID,
			ParentMessageId: parentID,
			CreatedAt:       tm.CreatedAt,
			CreatedByUserId: createdBy,
			Role:            toolRole,
			Type:            toolType,
			Content:         &body,
			ToolMessage:     []*agconv.ToolMessageView{normalizedToolMessage(tm, body)},
		})
	}
	return out
}

func normalizedToolMessage(tm *agconv.ToolMessageView, body string) *agconv.ToolMessageView {
	if tm == nil {
		return nil
	}
	clone := *tm
	if tm.ToolCall == nil {
		return &clone
	}
	toolCall := *tm.ToolCall
	if tm.ToolCall.ResponsePayload != nil {
		payload := *tm.ToolCall.ResponsePayload
		payload.InlineBody = &body
		payload.Compression = ""
		toolCall.ResponsePayload = &payload
	}
	clone.ToolCall = &toolCall
	return &clone
}

func (s *Service) toolMessageResponseBody(ctx context.Context, tm *agconv.ToolMessageView) string {
	if tm == nil || tm.ToolCall == nil || tm.ToolCall.ResponsePayload == nil {
		return ""
	}
	payloadID := strings.TrimSpace(tm.ToolCall.ResponsePayload.Id)
	if payloadID != "" && s != nil && s.conversation != nil {
		payload, err := s.conversation.GetPayload(ctx, payloadID)
		if err == nil && payload != nil && payload.InlineBody != nil && len(*payload.InlineBody) > 0 {
			if body := decodePayloadInlineBody(string(*payload.InlineBody), payload.Compression); body != "" {
				return body
			}
		}
	}
	if payload := tm.ToolCall.ResponsePayload; payload != nil {
		if inline := payload.InlineBody; inline != nil {
			if body := decodePayloadInlineBody(*inline, payload.Compression); body != "" {
				return body
			}
		}
	}
	return ""
}

func (s *Service) messageToolResultBody(ctx context.Context, msg *apiconv.Message) string {
	if msg == nil {
		return ""
	}
	for _, tm := range msg.ToolMessage {
		if body := s.toolMessageResponseBody(ctx, tm); body != "" {
			return body
		}
	}
	return strings.TrimSpace(msg.GetContent())
}

func decodePayloadInlineBody(inline string, compression string) string {
	if inline == "" {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(compression), "gzip") {
		return strings.TrimSpace(inline)
	}
	reader, err := gzip.NewReader(bytes.NewReader([]byte(inline)))
	if err != nil {
		return strings.TrimSpace(inline)
	}
	defer reader.Close()
	inflated, err := io.ReadAll(reader)
	if err != nil {
		return strings.TrimSpace(inline)
	}
	if decoded := strings.TrimSpace(string(inflated)); decoded != "" {
		return decoded
	}
	return strings.TrimSpace(inline)
}

func isAttachmentCarrier(msg *apiconv.Message) bool {
	if msg == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(msg.Type), "control") {
		return false
	}
	if messageToolCall(msg) != nil {
		return false
	}
	if msg.AttachmentPayloadId != nil && strings.TrimSpace(*msg.AttachmentPayloadId) != "" {
		return true
	}
	return len(msg.Attachment) > 0
}

func (s *Service) attachmentsFromMessage(ctx context.Context, msg *apiconv.Message, cache map[string]*prompt.Attachment) ([]*prompt.Attachment, error) {
	if msg == nil {
		return nil, nil
	}
	attachments := attachmentsFromMessageView(msg)

	if msg.AttachmentPayloadId == nil || strings.TrimSpace(*msg.AttachmentPayloadId) == "" {
		return attachments, nil
	}
	if s.conversation == nil {
		return nil, fmt.Errorf("conversation API not configured")
	}
	payloadID := strings.TrimSpace(*msg.AttachmentPayloadId)

	if cache != nil {
		if cached, ok := cache[payloadID]; ok && cached != nil {
			return append(attachments, cached), nil
		}
	}

	payload, err := s.conversation.GetPayload(ctx, payloadID)
	if err != nil {
		return nil, fmt.Errorf("get attachment payload %q: %w", payloadID, err)
	}
	if payload == nil {
		return nil, fmt.Errorf("get attachment payload %q: not found", payloadID)
	}
	var data []byte
	if payload.InlineBody != nil && len(*payload.InlineBody) > 0 {
		data = make([]byte, len(*payload.InlineBody))
		copy(data, *payload.InlineBody)
	} else if payload.URI != nil && strings.TrimSpace(*payload.URI) != "" {
		downloaded, err := afs.New().DownloadWithURL(ctx, strings.TrimSpace(*payload.URI))
		if err != nil {
			return nil, fmt.Errorf("download attachment payload uri %q: %w", strings.TrimSpace(*payload.URI), err)
		}
		data = downloaded
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("attachment payload %q has no data", payloadID)
	}

	name := ""
	if msg.Content != nil {
		name = strings.TrimSpace(*msg.Content)
	}
	if name == "" {
		name = "(attachment)"
	}
	uri := ""
	if payload.URI != nil {
		uri = strings.TrimSpace(*payload.URI)
	}
	mimeType := strings.TrimSpace(payload.MimeType)
	att := &prompt.Attachment{
		Name: name,
		URI:  uri,
		Mime: mimeType,
		Data: data,
	}
	debugAttachmentf("loaded attachment payload=%s bytes=%d mime=%s name=%s", payloadID, len(data), mimeType, name)
	if cache != nil {
		cache[payloadID] = att
	}
	attachments = append(attachments, att)
	return attachments, nil
}

func attachmentsFromMessageView(msg *apiconv.Message) []*prompt.Attachment {
	if msg == nil || msg.Attachment == nil || len(msg.Attachment) == 0 {
		return nil
	}
	defaultName := ""
	if msg.Content != nil && strings.EqualFold(strings.TrimSpace(msg.Type), "control") {
		defaultName = strings.TrimSpace(*msg.Content)
	}
	var attachments []*prompt.Attachment
	for _, av := range msg.Attachment {
		if av == nil {
			continue
		}
		var data []byte
		if av.InlineBody != nil && len(*av.InlineBody) > 0 {
			data = decodeAttachmentInlineBodyBytes(*av.InlineBody, av.Compression)
		} else {
			continue
		}
		uri := ""
		if av.Uri != nil {
			uri = strings.TrimSpace(*av.Uri)
		}
		name := defaultName
		if name == "" && uri != "" {
			name = path.Base(uri)
		}
		if name == "" {
			name = "(attachment)"
		}
		mimeType := strings.TrimSpace(av.MimeType)
		attachments = append(attachments, &prompt.Attachment{
			Name: name,
			URI:  uri,
			Mime: mimeType,
			Data: data,
		})
	}
	return attachments
}

func decodeAttachmentInlineBodyBytes(inline []byte, compression string) []byte {
	if len(inline) == 0 {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(compression), "gzip") {
		return append([]byte(nil), inline...)
	}
	reader, err := gzip.NewReader(bytes.NewReader(inline))
	if err != nil {
		return append([]byte(nil), inline...)
	}
	defer reader.Close()
	inflated, err := io.ReadAll(reader)
	if err != nil || len(inflated) == 0 {
		return append([]byte(nil), inline...)
	}
	return inflated
}
