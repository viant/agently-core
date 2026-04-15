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

	t.Run("failed latest turn with user-only message -> error", func(t *testing.T) {
		userStatus := "rejected"
		c := &ConversationView{Transcript: []*TranscriptView{{
			CreatedAt: now,
			Status:    "failed",
			Message: []*MessageView{{
				Id:        "m-user",
				Role:      "user",
				CreatedAt: now,
				Status:    &userStatus,
				Type:      "text",
			}},
		}}}
		c.OnRelation(nil)
		assert.EqualValues(t, StageError, c.Stage)
	})

	t.Run("failed latest turn without messages -> error", func(t *testing.T) {
		c := &ConversationView{Transcript: []*TranscriptView{{
			CreatedAt: now,
			Status:    "failed",
		}}}
		c.OnRelation(nil)
		assert.EqualValues(t, StageError, c.Stage)
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

func TestComputeTurnStage_ToolOriginatedPendingElicitation(t *testing.T) {
	now := time.Now()
	status := "pending"
	elicID := "elic-tool-1"
	tView := &TranscriptView{
		CreatedAt: now,
		Message: []*MessageView{{
			Id:            "tool-elic",
			Role:          "tool",
			Type:          "control",
			Status:        &status,
			CreatedAt:     now,
			ElicitationId: &elicID,
		}},
	}

	tView.OnRelation(nil)
	assert.EqualValues(t, StageEliciting, tView.Stage)
}

func TestComputeConversationStage_ToolOriginatedPendingElicitation(t *testing.T) {
	now := time.Now()
	status := "pending"
	elicID := "elic-tool-1"
	c := &ConversationView{Transcript: []*TranscriptView{{
		CreatedAt: now,
		Message: []*MessageView{{
			Id:            "tool-elic",
			Role:          "tool",
			Type:          "control",
			Status:        &status,
			CreatedAt:     now,
			ElicitationId: &elicID,
		}},
	}}}
	c.OnRelation(nil)
	assert.EqualValues(t, StageEliciting, c.Stage)
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

func TestComputeTurnStage_FailedTurnStatus(t *testing.T) {
	now := time.Now()

	t.Run("failed user-only turn -> error", func(t *testing.T) {
		userStatus := "rejected"
		turn := &TranscriptView{
			CreatedAt: now,
			Status:    "failed",
			Message: []*MessageView{{
				Id:        "m-user",
				Role:      "user",
				CreatedAt: now,
				Status:    &userStatus,
				Type:      "text",
			}},
		}
		assert.EqualValues(t, StageError, computeTurnStage(turn))
	})

	t.Run("failed empty turn -> error", func(t *testing.T) {
		turn := &TranscriptView{
			CreatedAt: now,
			Status:    "failed",
		}
		assert.EqualValues(t, StageError, computeTurnStage(turn))
	})
}

func TestComputeTurnStage_SucceededTurnStatusWinsOverMessageInference(t *testing.T) {
	now := time.Now()
	running := "running"
	turn := &TranscriptView{
		CreatedAt: now,
		Status:    "succeeded",
		Message: []*MessageView{{
			Id:        "assistant-1",
			Role:      "assistant",
			CreatedAt: now,
			Status:    &running,
			Type:      "text",
		}},
	}
	assert.EqualValues(t, StageDone, computeTurnStage(turn))
}

func TestComputeConversationStage_SucceededStatusWinsOverTranscriptInference(t *testing.T) {
	now := time.Now()
	running := "running"
	c := &ConversationView{
		Status: strPtr("succeeded"),
		Transcript: []*TranscriptView{{
			CreatedAt: now,
			Status:    "running",
			Message: []*MessageView{{
				Id:        "assistant-1",
				Role:      "assistant",
				CreatedAt: now,
				Status:    &running,
				Type:      "text",
			}},
		}},
	}
	c.OnRelation(nil)
	assert.EqualValues(t, StageDone, c.Stage)
	require.NotNil(t, c.Status)
	assert.Equal(t, StatusSucceeded, *c.Status)
}

func TestComputeConversationStage_LatestSucceededTurnWinsOverMessageInference(t *testing.T) {
	now := time.Now()
	running := "running"
	c := &ConversationView{Transcript: []*TranscriptView{{
		CreatedAt: now,
		Status:    "succeeded",
		Message: []*MessageView{{
			Id:        "assistant-1",
			Role:      "assistant",
			CreatedAt: now,
			Status:    &running,
			Type:      "text",
		}},
	}}}
	c.OnRelation(nil)
	assert.EqualValues(t, StageDone, c.Stage)
	require.NotNil(t, c.Status)
	assert.Equal(t, StatusSucceeded, *c.Status)
}

func TestComputeTurnStage_RunningStatusWinsOverTranscriptDoneInference(t *testing.T) {
	now := time.Now()
	turn := &TranscriptView{
		CreatedAt: now,
		Status:    "running",
		Message: []*MessageView{{
			Id:        "assistant-1",
			Role:      "assistant",
			CreatedAt: now,
			Type:      "text",
			Content:   strPtr("already has prose"),
		}},
	}
	assert.EqualValues(t, StageThinking, computeTurnStage(turn))
}

func TestComputeConversationStage_RunningStatusWinsOverTranscriptDoneInference(t *testing.T) {
	now := time.Now()
	c := &ConversationView{
		Status: strPtr("running"),
		Transcript: []*TranscriptView{{
			CreatedAt: now,
			Status:    "running",
			Message: []*MessageView{{
				Id:        "assistant-1",
				Role:      "assistant",
				CreatedAt: now,
				Type:      "text",
				Content:   strPtr("already has prose"),
			}},
		}},
	}
	c.OnRelation(nil)
	assert.EqualValues(t, StageThinking, c.Stage)
	require.NotNil(t, c.Status)
	assert.Equal(t, StatusRunning, *c.Status)
}

func strPtr(v string) *string { return &v }
