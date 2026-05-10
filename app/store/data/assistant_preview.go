package data

import (
	"context"
	"strings"
	"time"

	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
)

type AssistantPreview struct {
	Output             string
	Narration          string
	HasFinalResponse   bool
	OutputAt           time.Time
	NarrationAt        time.Time
	LastMessageAt      time.Time
	LastMessageKnown   bool
	ConversationID     string
	OutputMessageID    string
	NarrationMessageID string
}

func (p *AssistantPreview) PreferredText() string {
	if p == nil {
		return ""
	}
	if text := strings.TrimSpace(p.Output); text != "" {
		return text
	}
	return strings.TrimSpace(p.Narration)
}

func LatestAssistantPreview(ctx context.Context, svc Service, conversationID string) (*AssistantPreview, error) {
	conversationID = strings.TrimSpace(conversationID)
	if svc == nil || conversationID == "" {
		return nil, nil
	}
	preview := &AssistantPreview{ConversationID: conversationID}
	final, err := latestAssistantPreviewRow(ctx, svc, conversationID, true)
	if err != nil {
		return nil, err
	}
	if final != nil {
		preview.Output = strings.TrimSpace(previewString(final.Content))
		preview.HasFinalResponse = preview.Output != ""
		preview.OutputAt = final.CreatedAt
		preview.OutputMessageID = strings.TrimSpace(final.Id)
		preview.LastMessageAt = final.CreatedAt
		preview.LastMessageKnown = true
	}
	status, err := latestAssistantPreviewRow(ctx, svc, conversationID, false)
	if err != nil {
		return nil, err
	}
	if status != nil {
		text := strings.TrimSpace(previewString(status.Narration))
		if text == "" && status.Interim != 0 {
			text = strings.TrimSpace(previewString(status.Content))
		}
		preview.Narration = text
		preview.NarrationAt = status.CreatedAt
		preview.NarrationMessageID = strings.TrimSpace(status.Id)
		if !preview.LastMessageKnown || status.CreatedAt.After(preview.LastMessageAt) {
			preview.LastMessageAt = status.CreatedAt
			preview.LastMessageKnown = true
		}
	}
	return preview, nil
}

func latestAssistantPreviewRow(ctx context.Context, svc Service, conversationID string, final bool) (*agmessagelist.MessageRowsView, error) {
	input := &agmessagelist.MessageRowsInput{
		ConversationId: conversationID,
		Has: &agmessagelist.MessageRowsInputHas{
			ConversationId: true,
		},
	}
	if final {
		input.AssistantFinal = true
		input.Has.AssistantFinal = true
	} else {
		input.AssistantStatus = true
		input.Has.AssistantStatus = true
	}
	page, err := svc.GetMessagesPage(ctx, input, &PageInput{Limit: 1, Direction: DirectionLatest})
	if err != nil {
		return nil, err
	}
	if page == nil || len(page.Rows) == 0 {
		return nil, nil
	}
	return page.Rows[0], nil
}

func previewString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
