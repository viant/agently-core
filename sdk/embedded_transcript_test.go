package sdk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	agmessagelist "github.com/viant/agently-core/pkg/agently/message/list"
)

func TestFilterTranscriptSinceMessage_Inclusive(t *testing.T) {
	msg1 := &agconv.MessageView{Id: "m1", CreatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)}
	msg2 := &agconv.MessageView{Id: "m2", CreatedAt: time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC)}
	msg3 := &agconv.MessageView{Id: "m3", CreatedAt: time.Date(2026, 1, 1, 10, 2, 0, 0, time.UTC)}
	msg4 := &agconv.MessageView{Id: "m4", CreatedAt: time.Date(2026, 1, 1, 10, 3, 0, 0, time.UTC)}
	turn1 := &agconv.TranscriptView{Id: "turn-1", Message: []*agconv.MessageView{msg1, msg2, msg3}}
	turn2 := &agconv.TranscriptView{Id: "turn-2", Message: []*agconv.MessageView{msg4}}

	got := filterTranscriptSinceMessage(convstore.Transcript{(*convstore.Turn)(turn1), (*convstore.Turn)(turn2)}, "m2")
	require.Len(t, got, 2)
	require.Len(t, got[0].Message, 2)
	require.Equal(t, "m2", got[0].Message[0].Id)
	require.Equal(t, "m3", got[0].Message[1].Id)
	require.Equal(t, "m4", got[1].Message[0].Id)
}

func TestResolveElicitationPayload_ContentFallback(t *testing.T) {
	client := &backendClient{}
	got := client.resolveElicitationPayload(context.Background(), "elic-1", "", `{"message":"Pick one","requestedSchema":{"type":"object","properties":{"color":{"type":"string"}}}}`)
	require.NotNil(t, got)
	require.Equal(t, "elic-1", got["elicitationId"])
	require.Equal(t, "Pick one", got["message"])
}

func TestNormalizeMessagePage_CanonicalizesToolName(t *testing.T) {
	page := &MessagePage{
		Rows: []*agmessagelist.MessageRowsView{
			{ToolName: strPtr("system_os-getEnv")},
		},
	}

	normalizeMessagePage(page)

	require.NotNil(t, page.Rows[0].ToolName)
	require.Equal(t, "system/os/getEnv", *page.Rows[0].ToolName)
}

func TestEnrichTranscriptElicitations_NormalizesContentFromStructuredPayload(t *testing.T) {
	client := &backendClient{}
	elicitationID := "elic-1"
	msg := &agconv.MessageView{
		Id:            "m1",
		Content:       strPtr("map[message:Please provide your favorite color. requestedSchema:map[type:object]]"),
		ElicitationId: &elicitationID,
		Elicitation: map[string]interface{}{
			"message": "Please provide your favorite color.",
		},
	}
	turn := &agconv.TranscriptView{Id: "turn-1", Message: []*agconv.MessageView{msg}}

	client.enrichTranscriptElicitations(context.Background(), convstore.Transcript{(*convstore.Turn)(turn)})

	require.NotNil(t, msg.Elicitation)
	require.Equal(t, "Please provide your favorite color.", msg.Elicitation["message"])
	require.NotNil(t, msg.Content)
	require.Equal(t, "Please provide your favorite color.", *msg.Content)
}

func TestPruneTranscriptNoise_RemovesBlankInterimAssistant(t *testing.T) {
	content := "visible"
	turn := &agconv.TranscriptView{
		Id: "turn-1",
		Message: []*agconv.MessageView{
			{Id: "m1", Role: "assistant", Interim: 1},
			{Id: "m2", Role: "assistant", Content: &content},
		},
	}

	pruneTranscriptNoise(convstore.Transcript{(*convstore.Turn)(turn)})

	require.Len(t, turn.Message, 1)
	require.Equal(t, "m2", turn.Message[0].Id)
}

func TestBuildCanonicalState_ExecutionPagesPerModelMessage(t *testing.T) {
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	modelStatus := "completed"
	toolStatus := "completed"
	iteration1 := 1
	iteration2 := 2
	content1 := "I'm going to inspect the repository structure."
	content2 := "The repo is primarily Go code."

	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "succeeded",
		CreatedAt:      now,
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &content1,
				Iteration: &iteration1,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: modelStatus},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:              "tm1",
						ParentMessageId: strPtr("m1"),
						CreatedAt:       now.Add(time.Second),
						Sequence:        intPtr(1),
						Iteration:       &iteration1,
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm1",
							ToolName:  "resources-list",
							Status:    toolStatus,
						},
					},
					{
						Id:              "tm2",
						ParentMessageId: strPtr("m1"),
						CreatedAt:       now.Add(2 * time.Second),
						Sequence:        intPtr(2),
						Iteration:       &iteration1,
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm2",
							ToolName:  "resources-grepFiles",
							Status:    toolStatus,
						},
					},
				},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   0,
				Content:   &content2,
				Iteration: &iteration2,
				ModelCall: &agconv.ModelCallView{MessageId: "m2", Status: modelStatus},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	ts := state.Turns[0]
	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 2)

	first := ts.Execution.Pages[0]
	require.Equal(t, "m1", first.AssistantMessageID)
	require.Equal(t, 1, first.Iteration)
	require.Equal(t, content1, first.Narration)
	require.False(t, first.FinalResponse)
	require.Len(t, first.ToolSteps, 2)
	require.Equal(t, "resources-list", first.ToolSteps[0].ToolName)
	require.Equal(t, "resources-grepFiles", first.ToolSteps[1].ToolName)

	second := ts.Execution.Pages[1]
	require.Equal(t, "m2", second.AssistantMessageID)
	require.Equal(t, 2, second.Iteration)
	require.True(t, second.FinalResponse)
	require.Equal(t, content2, second.Content)
	require.Len(t, second.ToolSteps, 0)
}

func TestBuildCanonicalState_HidesIntakeRouterJSONUntilFinalResponse(t *testing.T) {
	iteration := 0
	routerJSON := `{"clarificationNeeded":true,"clarificationQuestion":"Which metric?"}`
	mode := "router"
	phase := "intake"
	status := "completed"

	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "running",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &routerJSON,
				Mode:      &mode,
				Phase:     &phase,
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{
					MessageId: "m1",
					Status:    status,
				},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	ts := state.Turns[0]
	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 1)

	page := ts.Execution.Pages[0]
	require.Equal(t, "intake", page.Phase)
	require.Equal(t, "intake", page.ExecutionRole)
	require.False(t, page.FinalResponse)
	require.Empty(t, page.Content)
}

func TestBuildCanonicalState_AttachesRootParentToolMessagesByIteration(t *testing.T) {
	iteration1 := 1
	modelStatus := "running"
	toolStatus := "completed"
	rootID := "root-1"
	narration := "Using resources-list."

	root := &agconv.MessageView{
		Id:   rootID,
		Role: "user",
		ToolMessage: []*agconv.ToolMessageView{
			{
				Id:              "tm1",
				ParentMessageId: strPtr(rootID),
				Sequence:        intPtr(2),
				Iteration:       &iteration1,
				ToolName:        strPtr("resources/list"),
				ToolCall: &agconv.ToolCallView{
					MessageId: "tm1",
					ToolName:  "resources/list",
					Status:    toolStatus,
				},
			},
		},
	}
	model := &agconv.MessageView{
		Id:              "m1",
		Role:            "assistant",
		Interim:         1,
		Content:         &narration,
		Iteration:       &iteration1,
		ParentMessageId: strPtr(rootID),
		ModelCall: &agconv.ModelCallView{
			MessageId: "m1",
			Status:    modelStatus,
		},
	}
	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "running",
		Message:        []*agconv.MessageView{root, model},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	ts := state.Turns[0]
	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 1)

	page := ts.Execution.Pages[0]
	require.Equal(t, "m1", page.AssistantMessageID)
	require.Equal(t, narration, page.Narration)
	require.Len(t, page.ToolSteps, 1)
	require.Equal(t, "resources/list", page.ToolSteps[0].ToolName)
}

func TestBuildCanonicalState_PreservesLinkedConversationOnToolStepAndTurn(t *testing.T) {
	iteration1 := 1
	linkedID := "child-conv-1"
	toolStatus := "completed"
	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   strPtr("Delegating to coder."),
				Iteration: &iteration1,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:                   "tm1",
						ParentMessageId:      strPtr("m1"),
						Iteration:            &iteration1,
						LinkedConversationId: &linkedID,
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm1",
							OpId:      "call-1",
							ToolName:  "llm/agents/run",
							Status:    toolStatus,
						},
					},
				},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)

	ts := state.Turns[0]
	require.Len(t, ts.LinkedConversations, 1)
	require.Equal(t, linkedID, ts.LinkedConversations[0].ConversationID)
	require.Equal(t, "call-1", ts.LinkedConversations[0].ToolCallID)

	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 1)
	require.Len(t, ts.Execution.Pages[0].ToolSteps, 1)
	require.Equal(t, linkedID, ts.Execution.Pages[0].ToolSteps[0].LinkedConversationID)
	require.Equal(t, linkedID, ts.Execution.Pages[0].ToolSteps[0].OperationID)
	require.NotNil(t, ts.Execution.Pages[0].ToolSteps[0].AsyncOperation)
	require.Equal(t, linkedID, ts.Execution.Pages[0].ToolSteps[0].AsyncOperation.OperationID)
}

func TestBuildCanonicalState_DerivesExecAsyncOperationFromResponsePayload(t *testing.T) {
	toolStatus := "completed"
	payloadID := "resp-1"
	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:      "m1",
				Role:    "assistant",
				Interim: 1,
				Content: strPtr("Running command."),
				ModelCall: &agconv.ModelCallView{
					MessageId: "m1",
					Status:    "completed",
				},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:              "tm1",
						ParentMessageId: strPtr("m1"),
						ToolCall: &agconv.ToolCallView{
							MessageId:         "tm1",
							OpId:              "call-1",
							ToolName:          "system/exec:start",
							Status:            toolStatus,
							ResponsePayloadId: &payloadID,
							ResponsePayload: &agconv.ModelCallStreamPayloadView{
								Id:         payloadID,
								InlineBody: strPtr(`{"sessionId":"sess-1","status":"completed"}`),
							},
						},
					},
				},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	require.NotNil(t, state.Turns[0].Execution)
	require.Len(t, state.Turns[0].Execution.Pages, 1)
	require.Len(t, state.Turns[0].Execution.Pages[0].ToolSteps, 1)
	step := state.Turns[0].Execution.Pages[0].ToolSteps[0]
	require.Equal(t, "sess-1", step.OperationID)
	require.NotNil(t, step.AsyncOperation)
	require.Equal(t, "sess-1", step.AsyncOperation.OperationID)
	require.Equal(t, "completed", step.AsyncOperation.Status)
	require.NotNil(t, step.AsyncOperation.Response)
}

func TestBuildCanonicalState_DerivesExecAsyncOperationTerminalStatusFromResponsePayload(t *testing.T) {
	testCases := []struct {
		name   string
		status string
		body   string
	}{
		{
			name:   "failed",
			status: "failed",
			body:   `{"sessionId":"sess-fail","status":"failed","stderr":"TERMINAL_FAILURE"}`,
		},
		{
			name:   "canceled",
			status: "canceled",
			body:   `{"sessionId":"sess-cancel","status":"canceled","stdout":"canceled"}`,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			payloadID := "resp-1"
			turn := &agconv.TranscriptView{
				Id:             "turn-1",
				ConversationId: "conv-1",
				Status:         "succeeded",
				Message: []*agconv.MessageView{
					{
						Id:      "m1",
						Role:    "assistant",
						Interim: 1,
						Content: strPtr("Running command."),
						ModelCall: &agconv.ModelCallView{
							MessageId: "m1",
							Status:    "completed",
						},
						ToolMessage: []*agconv.ToolMessageView{
							{
								Id:              "tm1",
								ParentMessageId: strPtr("m1"),
								ToolCall: &agconv.ToolCallView{
									MessageId:         "tm1",
									OpId:              "call-1",
									ToolName:          "system/exec:start",
									Status:            testCase.status,
									ResponsePayloadId: &payloadID,
									ResponsePayload: &agconv.ModelCallStreamPayloadView{
										Id:         payloadID,
										InlineBody: strPtr(testCase.body),
									},
								},
							},
						},
					},
				},
			}

			state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
			require.NotNil(t, state)
			require.Len(t, state.Turns, 1)
			require.NotNil(t, state.Turns[0].Execution)
			require.Len(t, state.Turns[0].Execution.Pages, 1)
			require.Len(t, state.Turns[0].Execution.Pages[0].ToolSteps, 1)

			step := state.Turns[0].Execution.Pages[0].ToolSteps[0]
			require.NotNil(t, step.AsyncOperation)
			require.Equal(t, testCase.status, step.Status)
			require.Equal(t, testCase.status, step.AsyncOperation.Status)
			require.NotEmpty(t, step.OperationID)
			require.NotNil(t, step.AsyncOperation.Response)
		})
	}
}

func TestBuildCanonicalState_PreservesModelPayloadsOnCanonicalStep(t *testing.T) {
	requestID := "req-1"
	responseID := "resp-1"
	providerRequestID := "preq-1"
	providerResponseID := "presp-1"
	streamID := "stream-1"
	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:      "m1",
				Role:    "assistant",
				Interim: 0,
				Content: strPtr("done"),
				ModelCall: &agconv.ModelCallView{
					MessageId:                        "m1",
					Provider:                         "openai",
					Model:                            "gpt-5.2",
					Status:                           "completed",
					RequestPayloadId:                 &requestID,
					ResponsePayloadId:                &responseID,
					ProviderRequestPayloadId:         &providerRequestID,
					ProviderResponsePayloadId:        &providerResponseID,
					StreamPayloadId:                  &streamID,
					ModelCallRequestPayload:          &agconv.ModelCallStreamPayloadView{Id: requestID, InlineBody: strPtr(`{"input":"hello"}`)},
					ModelCallResponsePayload:         &agconv.ModelCallStreamPayloadView{Id: responseID, InlineBody: strPtr(`{"output":"world"}`)},
					ModelCallProviderRequestPayload:  &agconv.ModelCallStreamPayloadView{Id: providerRequestID, InlineBody: strPtr(`{"provider":"request"}`)},
					ModelCallProviderResponsePayload: &agconv.ModelCallStreamPayloadView{Id: providerResponseID, InlineBody: strPtr(`{"provider":"response"}`)},
					ModelCallStreamPayload:           &agconv.ModelCallStreamPayloadView{Id: streamID, InlineBody: strPtr("stream body")},
				},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	require.NotNil(t, state.Turns[0].Execution)
	require.Len(t, state.Turns[0].Execution.Pages, 1)
	require.Len(t, state.Turns[0].Execution.Pages[0].ModelSteps, 1)

	step := state.Turns[0].Execution.Pages[0].ModelSteps[0]
	require.Equal(t, requestID, step.RequestPayloadID)
	require.Equal(t, responseID, step.ResponsePayloadID)
	require.Equal(t, providerRequestID, step.ProviderRequestPayloadID)
	require.Equal(t, providerResponseID, step.ProviderResponsePayloadID)
	require.Equal(t, streamID, step.StreamPayloadID)
	require.NotNil(t, step.RequestPayload)
	require.NotNil(t, step.ResponsePayload)
	require.NotNil(t, step.ProviderRequestPayload)
	require.NotNil(t, step.ProviderResponsePayload)
	require.NotNil(t, step.StreamPayload)
}

func TestBuildCanonicalState_ExtractsAssistantState(t *testing.T) {
	iteration1 := 1
	iteration2 := 2
	narration := "Let me check."
	final := "Here is the answer."

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &narration,
				Iteration: &iteration1,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   0,
				Content:   &final,
				Iteration: &iteration2,
				ModelCall: &agconv.ModelCallView{MessageId: "m2", Status: "completed"},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	ts := state.Turns[0]
	require.NotNil(t, ts.Assistant)
	require.NotNil(t, ts.Assistant.Narration)
	require.Equal(t, narration, ts.Assistant.Narration.Content)
	require.NotNil(t, ts.Assistant.Final)
	require.Equal(t, final, ts.Assistant.Final.Content)
}

func TestBuildCanonicalState_PrefersLatestInterimAssistantNarrationFromTranscript(t *testing.T) {
	iteration1 := 1
	iteration2 := 2
	modelNarration := "Let me check."
	statusNarration := "Reviewing the order’s targeted and excluded site lists now."
	final := "Here is the answer."
	execMode := "exec"

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "running",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &modelNarration,
				Narration: &modelNarration,
				Iteration: &iteration1,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   1,
				Narration: &statusNarration,
				Mode:      &execMode,
			},
			{
				Id:        "m3",
				Role:      "assistant",
				Interim:   0,
				Content:   &final,
				Iteration: &iteration2,
				ModelCall: &agconv.ModelCallView{MessageId: "m3", Status: "completed"},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	ts := state.Turns[0]
	require.NotNil(t, ts.Assistant)
	require.NotNil(t, ts.Assistant.Narration)
	require.Equal(t, "m2", ts.Assistant.Narration.MessageID)
	require.Equal(t, statusNarration, ts.Assistant.Narration.Content)
	require.NotNil(t, ts.Assistant.Final)
	require.Equal(t, final, ts.Assistant.Final.Content)
}

func TestBuildCanonicalState_PromotesNarratorInterimAssistantToExecutionPage(t *testing.T) {
	narratorMode := "narrator"
	narration := "Delegated child is still working through the file listing."

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "running",
		Message: []*agconv.MessageView{
			{
				Id:        "n1",
				Role:      "assistant",
				Interim:   1,
				Narration: &narration,
				Mode:      &narratorMode,
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	require.NotNil(t, state.Turns[0].Execution)
	require.Len(t, state.Turns[0].Execution.Pages, 1)

	page := state.Turns[0].Execution.Pages[0]
	require.Equal(t, "narrator", page.ExecutionRole)
	require.Equal(t, narratorMode, page.Mode)
	require.Equal(t, narration, page.Narration)
	require.Len(t, page.ModelSteps, 1)
	require.Equal(t, "narrator", page.ModelSteps[0].ExecutionRole)
	require.NotNil(t, page.ModelSteps[0].ResponsePayload)
	require.Contains(t, string(page.ModelSteps[0].ResponsePayload), narration)
}

func TestBuildCanonicalState_SkipsSummaryAssistantAsFinal(t *testing.T) {
	iteration1 := 1
	iteration2 := 2
	narration := "Let me check."
	final := "Here is the answer."
	summary := "Title: Summary\n\n- key point"
	summaryMode := "summary"

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &narration,
				Iteration: &iteration1,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   0,
				Content:   &final,
				Iteration: &iteration2,
				ModelCall: &agconv.ModelCallView{MessageId: "m2", Status: "completed"},
			},
			{
				Id:        "m3",
				Role:      "assistant",
				Interim:   0,
				Content:   &summary,
				Mode:      &summaryMode,
				ModelCall: &agconv.ModelCallView{MessageId: "m3", Status: "completed"},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	ts := state.Turns[0]
	require.NotNil(t, ts.Assistant)
	require.NotNil(t, ts.Assistant.Final)
	require.Equal(t, "m2", ts.Assistant.Final.MessageID)
	require.Equal(t, final, ts.Assistant.Final.Content)
	require.NotNil(t, ts.Execution)
	// Summary page is now included in execution pages with Mode="summary" so UI can render it.
	// Total: page for m1 (iteration 1), page for m2 (iteration 2), page for m3 (summary).
	require.Len(t, ts.Execution.Pages, 3)
	require.Equal(t, "m2", ts.Execution.Pages[1].AssistantMessageID)
	require.Equal(t, "summary", ts.Execution.Pages[2].Mode)
	require.Equal(t, "m3", ts.Execution.Pages[2].AssistantMessageID)
}

func TestBuildCanonicalState_DoesNotLetExecCompletionOverridePrimaryFinal(t *testing.T) {
	iteration := 1
	final := "I’m sorry, but I can’t complete this request."
	detached := "Detached data-analyst completed."
	execMode := "exec"
	completed := "completed"
	toolName := "llm/agents:start"

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   strPtr("Working on it."),
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   0,
				Content:   &final,
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{MessageId: "m2", Status: "completed"},
			},
			{
				Id:       "m3",
				Role:     "assistant",
				Interim:  0,
				Content:  &detached,
				Mode:     &execMode,
				Status:   &completed,
				ToolName: &toolName,
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	ts := state.Turns[0]
	require.NotNil(t, ts.Assistant)
	require.NotNil(t, ts.Assistant.Final)
	require.Equal(t, "m2", ts.Assistant.Final.MessageID)
	require.Equal(t, final, ts.Assistant.Final.Content)
	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 1)
	require.Equal(t, "m2", ts.Execution.Pages[0].FinalAssistantMessageID)
	require.Equal(t, final, ts.Execution.Pages[0].Content)
}

func TestBuildCanonicalState_NormalizesTranscriptStatuses(t *testing.T) {
	now := time.Date(2026, 4, 3, 18, 45, 0, 0, time.UTC)
	iteration := 1
	waiting := "waiting_for_user"
	cancelled := "cancelled"

	turn := &agconv.TranscriptView{
		Id:             "turn-1",
		ConversationId: "conv-1",
		Status:         "succeeded",
		CreatedAt:      now,
		Message: []*agconv.MessageView{
			{
				Id:            "elic-1",
				Role:          "assistant",
				Content:       strPtr("Need input"),
				Status:        &waiting,
				ElicitationId: strPtr("elicitation-1"),
				CreatedAt:     now.Add(time.Second),
			},
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   0,
				Content:   strPtr("done"),
				Iteration: &iteration,
				CreatedAt: now.Add(2 * time.Second),
				ModelCall: &agconv.ModelCallView{
					MessageId: "m1",
					Status:    "success",
				},
				ToolMessage: []*agconv.ToolMessageView{
					{
						Id:        "tm1",
						CreatedAt: now.Add(3 * time.Second),
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm1",
							OpId:      "call-1",
							ToolName:  "resources/read",
							Status:    "done",
						},
					},
					{
						Id:                   "tm2",
						CreatedAt:            now.Add(4 * time.Second),
						LinkedConversationId: strPtr("child-1"),
						ToolCall: &agconv.ToolCallView{
							MessageId: "tm2",
							OpId:      "call-2",
							ToolName:  "llm/agents/run",
							Status:    "terminated",
						},
					},
				},
			},
			{
				Id:            "elic-2",
				Role:          "assistant",
				Content:       strPtr("Cancelled input"),
				Status:        &cancelled,
				ElicitationId: strPtr("elicitation-2"),
				CreatedAt:     now.Add(5 * time.Second),
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)

	ts := state.Turns[0]
	require.Equal(t, TurnStatusCompleted, ts.Status)
	require.NotNil(t, ts.Execution)
	require.Len(t, ts.Execution.Pages, 1)
	require.Equal(t, "success", ts.Execution.Pages[0].Status)
	require.Len(t, ts.Execution.Pages[0].ModelSteps, 1)
	require.Equal(t, "success", ts.Execution.Pages[0].ModelSteps[0].Status)
	require.Len(t, ts.Execution.Pages[0].ToolSteps, 2)
	require.Equal(t, "done", ts.Execution.Pages[0].ToolSteps[0].Status)
	require.Equal(t, "terminated", ts.Execution.Pages[0].ToolSteps[1].Status)

	require.NotNil(t, ts.Elicitation)
	require.Equal(t, ElicitationStatusCanceled, ts.Elicitation.Status)
}

func TestBuildCanonicalState_PreservesMarkdownWhitespaceBoundaries(t *testing.T) {
	iteration := 1
	content := "0 recommendations saved for team review.\n\n## Highlights\n| A | B |\n|---|---|\n| 1 | 2 |\n"
	narration := "Working through the request.\n\n"

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Narration: &narration,
				Content:   &narration,
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{MessageId: "m1", Status: "completed"},
			},
			{
				Id:        "m2",
				Role:      "assistant",
				Interim:   0,
				Content:   &content,
				Iteration: &iteration,
				ModelCall: &agconv.ModelCallView{MessageId: "m2", Status: "completed"},
			},
		},
	}

	state := BuildCanonicalState("conv-1", convstore.Transcript{(*convstore.Turn)(turn)})
	require.NotNil(t, state)
	require.Len(t, state.Turns, 1)
	require.NotNil(t, state.Turns[0].Execution)
	require.Len(t, state.Turns[0].Execution.Pages, 1)

	page := state.Turns[0].Execution.Pages[0]
	require.Equal(t, narration, page.Narration)
	require.Equal(t, content, page.Content)
	require.NotNil(t, state.Turns[0].Assistant)
	require.NotNil(t, state.Turns[0].Assistant.Final)
	require.Equal(t, content, state.Turns[0].Assistant.Final.Content)
}

func TestBuildTranscriptSelectors(t *testing.T) {
	selectors := buildTranscriptQuerySelectors(map[string]*QuerySelector{
		TranscriptSelectorTurn:    {Limit: 1},
		TranscriptSelectorMessage: {Limit: 1, Offset: 2, OrderBy: "created_at ASC,id ASC"},
		TranscriptSelectorToolMessage: {
			Limit:   1,
			Offset:  1,
			OrderBy: "created_at ASC,id ASC",
		},
	})
	require.Len(t, selectors, 3)
	require.Equal(t, TranscriptSelectorTurn, selectors[0].Name)
	require.Equal(t, 1, selectors[0].QuerySelector.Limit)
	require.Equal(t, TranscriptSelectorMessage, selectors[1].Name)
	require.Equal(t, 2, selectors[1].QuerySelector.Offset)
	require.Equal(t, TranscriptSelectorToolMessage, selectors[2].Name)
	require.Equal(t, "created_at ASC,id ASC", selectors[2].QuerySelector.OrderBy)
}

func strPtr(value string) *string {
	return &value
}

func intPtr(value int) *int {
	return &value
}
