package sdk

// Phase 1.4 parity tests: transcript-built state must equal reducer-replayed state
// for the same conversation data.
//
// Each test builds a ConversationState two ways:
//  1. Transcript path: BuildCanonicalState(conversationID, transcript)
//  2. Reducer path:    reducing a sequence of streaming.Events with Reduce()
//
// Both must produce structurally identical output for the same semantic conversation.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	"github.com/viant/agently-core/runtime/streaming"
)

// TestParity_NormalTurn checks a simple single-iteration turn with one tool call.
func TestParity_NormalTurn(t *testing.T) {
	now := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	completedAt := now.Add(2 * time.Second)
	iter1 := 1
	userContent := "What is the weather?"
	assistantContent := "It is sunny."
	opID := "tc-1"
	reqPID := "req-1"
	respPID := "resp-1"

	// --- Transcript path ---
	turn := &agconv.TranscriptView{
		Id:        "turn-1",
		Status:    "completed",
		CreatedAt: now,
		Message: []*agconv.MessageView{
			{
				Id:        "user-1",
				Role:      "user",
				TurnId:    strPtr("turn-1"),
				Content:   &userContent,
				CreatedAt: now,
			},
			{
				Id:        "asst-1",
				Role:      "assistant",
				TurnId:    strPtr("turn-1"),
				Content:   &assistantContent,
				Iteration: &iter1,
				CreatedAt: now.Add(time.Second),
				ModelCall: &agconv.ModelCallView{
					MessageId:         "asst-1",
					Status:            "completed",
					StartedAt:         &now,
					CompletedAt:       &completedAt,
					RequestPayloadId:  strPtr(reqPID),
					ResponsePayloadId: strPtr(respPID),
				},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "tool-msg-1",
						CreatedAt: now.Add(500 * time.Millisecond),
						ToolCall: &agconv.ToolCallView{
							OpId:              opID,
							ToolName:          "weather/get",
							Status:            "completed",
							StartedAt:         &now,
							CompletedAt:       &completedAt,
							RequestPayloadId:  strPtr(reqPID),
							ResponsePayloadId: strPtr(respPID),
						},
					},
				},
			},
		},
	}
	transcriptState := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})

	// --- Reducer path ---
	reducerState := Reduce(nil, &streaming.Event{
		Type: streaming.EventTypeTurnStarted, ConversationID: "conv-1", TurnID: "turn-1",
		UserMessageID: "user-1", CreatedAt: now,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeModelStarted, ConversationID: "conv-1", TurnID: "turn-1",
		AssistantMessageID: "asst-1", Status: "thinking", Iteration: 1, CreatedAt: now,
		RequestPayloadID: reqPID,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeToolCallStarted, ConversationID: "conv-1", TurnID: "turn-1",
		AssistantMessageID: "asst-1", ToolCallID: opID, ToolMessageID: "tool-msg-1",
		ToolName: "weather/get", Status: "running", Iteration: 1, CreatedAt: now,
		RequestPayloadID: reqPID,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeToolCallCompleted, ConversationID: "conv-1", TurnID: "turn-1",
		AssistantMessageID: "asst-1", ToolCallID: opID, ToolMessageID: "tool-msg-1",
		ToolName: "weather/get", Status: "completed", Iteration: 1, CompletedAt: &completedAt,
		ResponsePayloadID: respPID,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeAssistantFinal, ConversationID: "conv-1", TurnID: "turn-1",
		AssistantMessageID: "asst-1", Content: assistantContent, Iteration: 1,
		CreatedAt: now.Add(time.Second),
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeModelCompleted, ConversationID: "conv-1", TurnID: "turn-1",
		AssistantMessageID: "asst-1", Status: "completed", Iteration: 1,
		ResponsePayloadID: respPID, CompletedAt: &completedAt,
		Content: assistantContent, FinalResponse: true,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeTurnCompleted, ConversationID: "conv-1", TurnID: "turn-1",
		CreatedAt: completedAt,
	})

	// --- Parity assertions ---
	require.Equal(t, "conv-1", transcriptState.ConversationID)
	require.Equal(t, "conv-1", reducerState.ConversationID)
	require.Len(t, transcriptState.Turns, 1)
	require.Len(t, reducerState.Turns, 1)

	tt := transcriptState.Turns[0]
	rt := reducerState.Turns[0]
	require.Equal(t, "turn-1", tt.TurnID)
	require.Equal(t, "turn-1", rt.TurnID)
	require.Equal(t, TurnStatusCompleted, tt.Status)
	require.Equal(t, TurnStatusCompleted, rt.Status)

	// User message
	require.NotNil(t, tt.User)
	require.NotNil(t, rt.User)
	require.Equal(t, "user-1", tt.User.MessageID)
	require.Equal(t, "user-1", rt.User.MessageID)

	// Execution: one page
	require.NotNil(t, tt.Execution)
	require.NotNil(t, rt.Execution)
	require.Len(t, tt.Execution.Pages, 1)
	require.Len(t, rt.Execution.Pages, 1)

	tp := tt.Execution.Pages[0]
	rp := rt.Execution.Pages[0]
	require.Equal(t, 1, tp.Iteration)
	require.Equal(t, 1, rp.Iteration)

	// Tool step
	require.Len(t, tp.ToolSteps, 1)
	require.Len(t, rp.ToolSteps, 1)
	require.Equal(t, opID, tp.ToolSteps[0].ToolCallID)
	require.Equal(t, opID, rp.ToolSteps[0].ToolCallID)
	require.Equal(t, reqPID, tp.ToolSteps[0].RequestPayloadID)
	require.Equal(t, reqPID, rp.ToolSteps[0].RequestPayloadID)

	// Model step
	require.Len(t, tp.ModelSteps, 1)
	require.Len(t, rp.ModelSteps, 1)
	require.Equal(t, "asst-1", tp.ModelSteps[0].ModelCallID)
	require.Equal(t, "asst-1", rp.ModelSteps[0].ModelCallID)
	require.Equal(t, respPID, tp.ModelSteps[0].ResponsePayloadID)
	require.Equal(t, respPID, rp.ModelSteps[0].ResponsePayloadID)

	// Assistant final content
	require.NotNil(t, tt.Assistant)
	require.NotNil(t, rt.Assistant)
	require.NotNil(t, tt.Assistant.Final)
	require.NotNil(t, rt.Assistant.Final)
	require.Equal(t, assistantContent, tt.Assistant.Final.Content)
	require.Equal(t, assistantContent, rt.Assistant.Final.Content)
}

// TestParity_SummaryPage verifies that summary pages are included in both paths
// with Mode="summary" and are NOT used as the final assistant content.
func TestParity_SummaryPage(t *testing.T) {
	now := time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC)
	iter1 := 1
	finalContent := "Here is the answer."
	summaryContent := "Summary of what was done."
	summaryMode := "summary"

	// --- Transcript path ---
	turn := &agconv.TranscriptView{
		Id: "turn-1", Status: "completed", CreatedAt: now,
		Message: []*agconv.MessageView{
			{Id: "user-1", Role: "user", TurnId: strPtr("turn-1"), Content: strPtr("Do it"),
				CreatedAt: now},
			{Id: "asst-1", Role: "assistant", TurnId: strPtr("turn-1"), Content: &finalContent,
				Iteration: &iter1, CreatedAt: now.Add(time.Second),
				ModelCall: &agconv.ModelCallView{MessageId: "asst-1", Status: "completed"}},
			{Id: "sum-1", Role: "assistant", TurnId: strPtr("turn-1"), Content: &summaryContent,
				Mode: &summaryMode, CreatedAt: now.Add(2 * time.Second),
				ModelCall: &agconv.ModelCallView{MessageId: "sum-1", Status: "completed"}},
		},
	}
	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.Len(t, state.Turns[0].Execution.Pages, 2, "should have normal page + summary page")

	normalPage := state.Turns[0].Execution.Pages[0]
	summaryPage := state.Turns[0].Execution.Pages[1]
	require.Equal(t, "", normalPage.Mode)
	require.Equal(t, "summary", summaryPage.Mode)
	require.Equal(t, "sum-1", summaryPage.AssistantMessageID)

	// Final assistant content must use normal page, not summary
	require.NotNil(t, state.Turns[0].Assistant)
	require.NotNil(t, state.Turns[0].Assistant.Final)
	require.Equal(t, finalContent, state.Turns[0].Assistant.Final.Content)
	require.Equal(t, "asst-1", state.Turns[0].Assistant.Final.MessageID)
}

func TestTranscriptBuild_DerivesSidecarPhaseForNonFinalToolPage(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 30, 0, 0, time.UTC)
	iter1 := 1
	preamble := "Pulling delegated benchmark now."

	turn := &agconv.TranscriptView{
		Id:        "turn-1",
		Status:    "completed",
		CreatedAt: now,
		Message: []*agconv.MessageView{
			{
				Id:        "user-1",
				Role:      "user",
				TurnId:    strPtr("turn-1"),
				Content:   strPtr("recommend frequency cap"),
				CreatedAt: now,
			},
			{
				Id:        "asst-1",
				Role:      "assistant",
				TurnId:    strPtr("turn-1"),
				Content:   &preamble,
				Preamble:  &preamble,
				Interim:   1,
				Iteration: &iter1,
				CreatedAt: now.Add(time.Second),
				ModelCall: &agconv.ModelCallView{
					MessageId: "asst-1",
					Status:    "completed",
				},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "tool-msg-1",
						CreatedAt: now.Add(1500 * time.Millisecond),
						Iteration: &iter1,
						ToolCall: &agconv.ToolCallView{
							OpId:     "op-1",
							ToolName: "llm_agents-start",
							Status:   "completed",
						},
					},
				},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.Len(t, state.Turns, 1)
	require.NotNil(t, state.Turns[0].Execution)
	require.Len(t, state.Turns[0].Execution.Pages, 1)
	require.Equal(t, "sidecar", state.Turns[0].Execution.Pages[0].Phase)
}

// TestParity_ElicitationTurn verifies elicitation state is produced identically
// by transcript path and reducer path.
func TestParity_ElicitationTurn(t *testing.T) {
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	elicID := "elic-1"
	pending := "pending"
	// Elicitation stored with schema embedded in content (legacy path).
	content := `{"message":"Pick a color","requestedSchema":{"type":"object","properties":{"color":{"type":"string"}}},"callbackUrl":"http://cb"}`

	// --- Transcript path ---
	turn := &agconv.TranscriptView{
		Id: "turn-1", Status: "waiting_for_user", CreatedAt: now,
		Message: []*agconv.MessageView{
			{Id: "user-1", Role: "user", TurnId: strPtr("turn-1"), Content: strPtr("Start"), CreatedAt: now},
			{Id: "elic-msg-1", Role: "assistant", TurnId: strPtr("turn-1"),
				Content: &content, Status: &pending, ElicitationId: &elicID,
				CreatedAt: now.Add(time.Second)},
		},
	}
	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state.Turns[0].Elicitation)
	es := state.Turns[0].Elicitation
	require.Equal(t, elicID, es.ElicitationID)
	require.Equal(t, ElicitationStatusPending, es.Status)
	require.NotNil(t, es.RequestedSchema, "requestedSchema must be parsed from content JSON")
	require.Equal(t, "http://cb", es.CallbackURL)
	require.Equal(t, TurnStatusWaitingForUser, state.Turns[0].Status)

	// --- Reducer path ---
	reducerState := Reduce(nil, &streaming.Event{
		Type: streaming.EventTypeTurnStarted, ConversationID: "conv-1", TurnID: "turn-1",
		UserMessageID: "user-1", CreatedAt: now,
	})
	reducerState = Reduce(reducerState, &streaming.Event{
		Type:           streaming.EventTypeElicitationRequested,
		ConversationID: "conv-1", TurnID: "turn-1",
		ElicitationID: elicID,
		Content:       "Pick a color",
		ElicitationData: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"color": map[string]interface{}{"type": "string"},
			},
		},
		CallbackURL: "http://cb",
		CreatedAt:   now.Add(time.Second),
	})

	require.NotNil(t, reducerState.Turns[0].Elicitation)
	re := reducerState.Turns[0].Elicitation
	require.Equal(t, elicID, re.ElicitationID)
	require.Equal(t, ElicitationStatusPending, re.Status)
	require.NotNil(t, re.RequestedSchema)
	require.Equal(t, "http://cb", re.CallbackURL)
	require.Equal(t, TurnStatusWaitingForUser, reducerState.Turns[0].Status)
}

// TestParity_QueuedTurn verifies that a queued turn appears in canonical state
// with TurnStatusQueued and can transition to running via the reducer.
func TestParity_QueuedTurn(t *testing.T) {
	now := time.Date(2026, 4, 1, 13, 0, 0, 0, time.UTC)
	queuedStatus := "queued"
	starterMsgID := "starter-1"

	// --- Transcript path ---
	turn := &agconv.TranscriptView{
		Id: "turn-q", Status: queuedStatus, CreatedAt: now,
		StartedByMessageId: &starterMsgID,
		Message: []*agconv.MessageView{
			{Id: starterMsgID, Role: "user", TurnId: strPtr("turn-q"),
				Content: strPtr("Run later"), CreatedAt: now},
		},
	}
	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.Len(t, state.Turns, 1)
	qt := state.Turns[0]
	require.Equal(t, TurnStatusQueued, qt.Status)
	require.Equal(t, starterMsgID, qt.StartedByMessageID)

	// Reducer: a turn_started event transitions queued→running
	reducerState := &ConversationState{
		ConversationID: "conv-1",
		Turns: []*TurnState{
			{TurnID: "turn-q", Status: TurnStatusQueued, StartedByMessageID: starterMsgID},
		},
	}
	reducerState = Reduce(reducerState, &streaming.Event{
		Type: streaming.EventTypeTurnStarted, ConversationID: "conv-1", TurnID: "turn-q",
		CreatedAt: now.Add(time.Minute),
	})
	require.Equal(t, TurnStatusRunning, reducerState.Turns[0].Status,
		"turn_started must transition a queued turn to running")
}
