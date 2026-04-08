package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type dedupeConvClient struct {
	conversation *apiconv.Conversation
}

func (d *dedupeConvClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return d.conversation, nil
}

func (d *dedupeConvClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (d *dedupeConvClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (d *dedupeConvClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (d *dedupeConvClient) PatchPayload(context.Context, *apiconv.MutablePayload) error { return nil }
func (d *dedupeConvClient) PatchMessage(context.Context, *apiconv.MutableMessage) error { return nil }
func (d *dedupeConvClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (d *dedupeConvClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (d *dedupeConvClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (d *dedupeConvClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error { return nil }
func (d *dedupeConvClient) PatchTurn(context.Context, *apiconv.MutableTurn) error         { return nil }
func (d *dedupeConvClient) DeleteConversation(context.Context, string) error              { return nil }
func (d *dedupeConvClient) DeleteMessage(context.Context, string, string) error           { return nil }

func TestShouldSkipFinalAssistantPersist(t *testing.T) {
	turnID := "turn-1"
	now := time.Now()
	content := "Hello! How can I assist you today?"

	makeConversation := func(messageContent string, interim int) *apiconv.Conversation {
		return &apiconv.Conversation{
			Id: "conv-1",
			Transcript: []*agconv.TranscriptView{
				{
					Id:             turnID,
					ConversationId: "conv-1",
					CreatedAt:      now,
					Message: []*agconv.MessageView{
						{
							Id:             "msg-1",
							ConversationId: "conv-1",
							CreatedAt:      now,
							Role:           "assistant",
							Type:           "text",
							Interim:        interim,
							TurnId:         dedupeStrPtr(turnID),
							Content:        dedupeStrPtr(messageContent),
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name      string
		conv      *apiconv.Conversation
		wantSkip  bool
		inContent string
	}{
		{
			name:      "skip when same finalized assistant content already exists",
			conv:      makeConversation(content, 0),
			wantSkip:  true,
			inContent: content,
		},
		{
			name:      "do not skip when same content is interim",
			conv:      makeConversation(content, 1),
			wantSkip:  false,
			inContent: content,
		},
		{
			name:      "do not skip when finalized content differs",
			conv:      makeConversation("Different response", 0),
			wantSkip:  false,
			inContent: content,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &dedupeConvClient{conversation: tc.conv}
			turn := &memory.TurnMeta{ConversationID: "conv-1", TurnID: turnID}
			got := shouldSkipFinalAssistantPersist(context.Background(), client, turn, tc.inContent)
			assert.Equal(t, tc.wantSkip, got)
		})
	}
}

func dedupeStrPtr(value string) *string {
	return &value
}
