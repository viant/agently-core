package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	convcli "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type interimArchiveConvClient struct {
	conversation *apiconv.Conversation
}

func (c *interimArchiveConvClient) GetConversation(context.Context, string, ...convcli.Option) (*apiconv.Conversation, error) {
	return c.conversation, nil
}

func (c *interimArchiveConvClient) GetConversations(context.Context, *convcli.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (c *interimArchiveConvClient) PatchConversations(context.Context, *convcli.MutableConversation) error {
	return nil
}

func (c *interimArchiveConvClient) GetPayload(context.Context, string) (*convcli.Payload, error) {
	return nil, nil
}

func (c *interimArchiveConvClient) PatchPayload(context.Context, *convcli.MutablePayload) error {
	return nil
}

func (c *interimArchiveConvClient) PatchMessage(_ context.Context, message *convcli.MutableMessage) error {
	if c.conversation == nil || message == nil {
		return nil
	}
	for _, turn := range c.conversation.Transcript {
		if turn == nil {
			continue
		}
		for _, existing := range turn.Message {
			if existing == nil || existing.Id != message.Id {
				continue
			}
			if message.Has != nil && message.Has.Archived && message.Archived != nil {
				existing.Archived = message.Archived
			}
			if message.Has != nil && message.Has.SupersededBy {
				existing.SupersededBy = message.SupersededBy
			}
			if message.Has != nil && message.Has.Interim && message.Interim != nil {
				existing.Interim = *message.Interim
			}
			return nil
		}
	}
	return nil
}

func (c *interimArchiveConvClient) GetMessage(context.Context, string, ...convcli.Option) (*convcli.Message, error) {
	return nil, nil
}

func (c *interimArchiveConvClient) GetMessageByElicitation(context.Context, string, string) (*convcli.Message, error) {
	return nil, nil
}

func (c *interimArchiveConvClient) PatchModelCall(context.Context, *convcli.MutableModelCall) error {
	return nil
}

func (c *interimArchiveConvClient) PatchToolCall(context.Context, *convcli.MutableToolCall) error {
	return nil
}

func (c *interimArchiveConvClient) PatchTurn(context.Context, *convcli.MutableTurn) error {
	return nil
}

func (c *interimArchiveConvClient) DeleteConversation(context.Context, string) error {
	return nil
}

func (c *interimArchiveConvClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func TestService_archiveOlderInterimAssistantMessages_ArchivesTaskModeRows(t *testing.T) {
	t.Parallel()

	now := time.Now()
	conversation := &apiconv.Conversation{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-1",
				CreatedAt:      now,
				Message: []*agconv.MessageView{
					{
						Id:             "assistant-old",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-time.Minute),
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("task"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
					{
						Id:             "assistant-new",
						ConversationId: "conv-1",
						CreatedAt:      now,
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("task"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
				},
			},
		},
	}

	client := &interimArchiveConvClient{conversation: conversation}
	svc := &Service{conversation: client}

	svc.archiveOlderInterimAssistantMessages(context.Background(), "conv-1", "turn-1", "assistant-new")

	oldMsg := conversation.Transcript[0].Message[0]
	newMsg := conversation.Transcript[0].Message[1]
	require.NotNil(t, oldMsg.Archived)
	assert.Equal(t, 1, *oldMsg.Archived)
	require.NotNil(t, oldMsg.SupersededBy)
	assert.Equal(t, "assistant-new", *oldMsg.SupersededBy)
	assert.Nil(t, newMsg.Archived)
}

func TestService_archiveOlderInterimAssistantMessages_SkipsSummaryAndRouterRows(t *testing.T) {
	t.Parallel()

	now := time.Now()
	conversation := &apiconv.Conversation{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-1",
				CreatedAt:      now,
				Message: []*agconv.MessageView{
					{
						Id:             "summary-row",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-2 * time.Minute),
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("summary"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
					{
						Id:             "router-row",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-time.Minute),
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("router"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
					{
						Id:             "assistant-new",
						ConversationId: "conv-1",
						CreatedAt:      now,
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("task"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
				},
			},
		},
	}

	client := &interimArchiveConvClient{conversation: conversation}
	svc := &Service{conversation: client}

	svc.archiveOlderInterimAssistantMessages(context.Background(), "conv-1", "turn-1", "assistant-new")

	assert.Nil(t, conversation.Transcript[0].Message[0].Archived)
	assert.Nil(t, conversation.Transcript[0].Message[1].Archived)
}

func TestService_archiveOlderInterimAssistantMessages_FinalPatchSupersedesOlderTaskRows(t *testing.T) {
	t.Parallel()

	now := time.Now()
	conversation := &apiconv.Conversation{
		Id: "conv-1",
		Transcript: []*agconv.TranscriptView{
			{
				Id:             "turn-1",
				ConversationId: "conv-1",
				CreatedAt:      now,
				Message: []*agconv.MessageView{
					{
						Id:             "assistant-old",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-time.Minute),
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("task"),
						TurnId:         steerPtr("turn-1"),
						Interim:        1,
					},
					{
						Id:             "assistant-final",
						ConversationId: "conv-1",
						CreatedAt:      now,
						Role:           "assistant",
						Type:           "text",
						Mode:           steerPtr("task"),
						TurnId:         steerPtr("turn-1"),
						Interim:        0,
					},
				},
			},
		},
	}
	client := &interimArchiveConvClient{conversation: conversation}
	svc := &Service{conversation: client}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"})

	svc.archiveOlderInterimAssistantMessages(ctx, "conv-1", "turn-1", "assistant-final")

	oldMsg := conversation.Transcript[0].Message[0]
	require.NotNil(t, oldMsg.Archived)
	assert.Equal(t, 1, *oldMsg.Archived)
}
