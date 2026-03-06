package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/runtime/memory"
)

func TestService_latestTurnTaskCheckpoint(t *testing.T) {
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
						Id:             "user-1",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-2 * time.Minute),
						Role:           "user",
						Type:           "task",
						TurnId:         steerPtr("turn-1"),
					},
					{
						Id:             "assistant-1",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(-time.Minute),
						Role:           "assistant",
						Type:           "text",
						TurnId:         steerPtr("turn-1"),
					},
					{
						Id:             "user-2",
						ConversationId: "conv-1",
						CreatedAt:      now,
						Role:           "user",
						Type:           "task",
						TurnId:         steerPtr("turn-1"),
					},
				},
			},
			{
				Id:             "turn-2",
				ConversationId: "conv-1",
				CreatedAt:      now,
				Message: []*agconv.MessageView{
					{
						Id:             "other-turn-task",
						ConversationId: "conv-1",
						CreatedAt:      now.Add(time.Minute),
						Role:           "user",
						Type:           "task",
						TurnId:         steerPtr("turn-2"),
					},
				},
			},
		},
	}

	svc := &Service{conversation: &dedupeConvClient{conversation: conversation}}
	checkpoint, err := svc.latestTurnTaskCheckpoint(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	require.NoError(t, err)
	require.True(t, checkpoint.Found)
	assert.Equal(t, "user-2", checkpoint.MessageID)
	assert.Equal(t, now, checkpoint.CreatedAt)
}

func TestService_hasNewTurnTaskSince(t *testing.T) {
	t.Parallel()

	now := time.Now()
	makeConversation := func(latestID string, latestAt time.Time) *apiconv.Conversation {
		return &apiconv.Conversation{
			Id: "conv-1",
			Transcript: []*agconv.TranscriptView{
				{
					Id:             "turn-1",
					ConversationId: "conv-1",
					CreatedAt:      now,
					Message: []*agconv.MessageView{
						{
							Id:             "user-1",
							ConversationId: "conv-1",
							CreatedAt:      now.Add(-time.Minute),
							Role:           "user",
							Type:           "task",
							TurnId:         steerPtr("turn-1"),
						},
						{
							Id:             latestID,
							ConversationId: "conv-1",
							CreatedAt:      latestAt,
							Role:           "user",
							Type:           "task",
							TurnId:         steerPtr("turn-1"),
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name       string
		checkpoint turnTaskCheckpoint
		conv       *apiconv.Conversation
		want       bool
	}{
		{
			name:       "no new task after checkpoint",
			checkpoint: turnTaskCheckpoint{MessageID: "user-2", CreatedAt: now, Found: true},
			conv:       makeConversation("user-2", now),
			want:       false,
		},
		{
			name:       "newer task by time triggers follow-up",
			checkpoint: turnTaskCheckpoint{MessageID: "user-2", CreatedAt: now, Found: true},
			conv:       makeConversation("steer-1", now.Add(time.Second)),
			want:       true,
		},
		{
			name:       "same timestamp but larger id triggers follow-up",
			checkpoint: turnTaskCheckpoint{MessageID: "steer-1", CreatedAt: now, Found: true},
			conv:       makeConversation("steer-2", now),
			want:       true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &Service{conversation: &dedupeConvClient{conversation: tc.conv}}
			got, err := svc.hasNewTurnTaskSince(context.Background(), memory.TurnMeta{
				ConversationID: "conv-1",
				TurnID:         "turn-1",
			}, tc.checkpoint)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEffectiveFollowUpCheckpoint(t *testing.T) {
	t.Parallel()

	initial := turnTaskCheckpoint{MessageID: "initial", CreatedAt: time.Now(), Found: true}
	processed := turnTaskCheckpoint{MessageID: "processed", CreatedAt: initial.CreatedAt.Add(time.Second), Found: true}

	t.Run("prefers last processed checkpoint from output", func(t *testing.T) {
		t.Parallel()
		output := &QueryOutput{lastTaskCheckpoint: processed}
		got := effectiveFollowUpCheckpoint(initial, output)
		assert.Equal(t, processed, got)
	})

	t.Run("falls back to initial checkpoint when output is empty", func(t *testing.T) {
		t.Parallel()
		got := effectiveFollowUpCheckpoint(initial, &QueryOutput{})
		assert.Equal(t, initial, got)
	})

	t.Run("falls back to initial checkpoint when output is nil", func(t *testing.T) {
		t.Parallel()
		got := effectiveFollowUpCheckpoint(initial, nil)
		assert.Equal(t, initial, got)
	})
}

func steerPtr(value string) *string {
	return &value
}
