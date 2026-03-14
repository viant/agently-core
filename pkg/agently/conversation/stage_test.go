package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeStage_ElicitationAndCancelSemantics(t *testing.T) {
	now := time.Now()
	mkMsg := func(status string) *MessageView {
		s := status
		role := "assistant"
		id := "m1"
		elic := "elic-1"
		return &MessageView{
			Id:            id,
			Role:          role,
			CreatedAt:     now,
			Status:        &s,
			ElicitationId: &elic,
			Interim:       0,
			Type:          "text",
		}
	}

	t.Run("pending -> eliciting", func(t *testing.T) {
		c := &ConversationView{Transcript: []*TranscriptView{{
			CreatedAt: now,
			Message:   []*MessageView{mkMsg("pending")},
		}}}
		c.OnRelation(nil)
		assert.EqualValues(t, StageEliciting, c.Stage)
	})

	t.Run("rejected -> error", func(t *testing.T) {
		c := &ConversationView{Transcript: []*TranscriptView{{
			CreatedAt: now,
			Message:   []*MessageView{mkMsg("rejected")},
		}}}
		c.OnRelation(nil)
		assert.EqualValues(t, StageError, c.Stage)
	})

	t.Run("canceled turn -> canceled", func(t *testing.T) {
		c := &ConversationView{Transcript: []*TranscriptView{{
			CreatedAt: now,
			Status:    "canceled",
			Message:   []*MessageView{mkMsg("canceled")},
		}}}
		c.OnRelation(nil)
		assert.EqualValues(t, StageCanceled, c.Stage)
	})

	t.Run("canceled conversation -> canceled", func(t *testing.T) {
		status := "canceled"
		c := &ConversationView{
			Status: &status,
			Transcript: []*TranscriptView{{
				CreatedAt: now,
				Message:   []*MessageView{mkMsg("pending")},
			}},
		}
		c.OnRelation(nil)
		assert.EqualValues(t, StageCanceled, c.Stage)
	})
}

func TestComputeTurnStage_UsesToolMessages(t *testing.T) {
	now := time.Now()
	msgStatus := "running"
	toolStatus := "running"
	tView := &TranscriptView{
		CreatedAt: now,
		Message: []*MessageView{{
			Id:        "assistant-1",
			Role:      "assistant",
			Type:      "text",
			Status:    &msgStatus,
			CreatedAt: now,
			ToolMessage: []*ToolMessageView{{
				Id:        "tool-msg-1",
				Type:      "tool_op",
				CreatedAt: now.Add(time.Second),
				ToolCall: &ToolCallView{
					Status: toolStatus,
				},
			}},
		}},
	}

	tView.OnRelation(nil)
	assert.EqualValues(t, StageExecuting, tView.Stage)
}

func TestComputeTurnStage_CanceledAssistantMessage(t *testing.T) {
	now := time.Now()
	status := "canceled"
	tView := &TranscriptView{
		CreatedAt: now,
		Message: []*MessageView{{
			Id:        "assistant-1",
			Role:      "assistant",
			Type:      "text",
			Status:    &status,
			CreatedAt: now,
		}},
	}

	tView.OnRelation(nil)
	assert.EqualValues(t, StageCanceled, tView.Stage)
}

func TestTranscriptOnRelation_PopulatesElicitationFromUserElicitationData(t *testing.T) {
	now := time.Now()
	elicID := "elic-1"
	content := "{\"message\":\"Pick a color\",\"requestedSchema\":{\"type\":\"object\"}}"
	tView := &TranscriptView{
		CreatedAt: now,
		Message: []*MessageView{{
			Id:            "assistant-elic",
			Role:          "assistant",
			Type:          "text",
			CreatedAt:     now,
			Content:       &content,
			ElicitationId: &elicID,
			UserElicitationData: &UserElicitationDataView{
				InlineBody:  &content,
				Compression: "none",
				MessageId:   "assistant-elic",
			},
		}},
	}

	tView.OnRelation(nil)
	require.Len(t, tView.Message, 1)
	require.NotNil(t, tView.Message[0].Elicitation)
	assert.Equal(t, elicID, tView.Message[0].Elicitation["elicitationId"])
	assert.Equal(t, "Pick a color", tView.Message[0].Elicitation["message"])
}
