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
	client := &EmbeddedClient{}
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
	client := &EmbeddedClient{}
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
	require.Equal(t, content1, first.Preamble)
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

func TestBuildCanonicalState_AttachesRootParentToolMessagesByIteration(t *testing.T) {
	iteration1 := 1
	modelStatus := "running"
	toolStatus := "completed"
	rootID := "root-1"
	preamble := "Using resources-list."

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
		Content:         &preamble,
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
	require.Equal(t, preamble, page.Preamble)
	require.Len(t, page.ToolSteps, 1)
	require.Equal(t, "resources/list", page.ToolSteps[0].ToolName)
}

func TestBuildCanonicalState_ExtractsAssistantState(t *testing.T) {
	iteration1 := 1
	iteration2 := 2
	preamble := "Let me check."
	final := "Here is the answer."

	turn := &agconv.TranscriptView{
		Id:     "turn-1",
		Status: "succeeded",
		Message: []*agconv.MessageView{
			{
				Id:        "m1",
				Role:      "assistant",
				Interim:   1,
				Content:   &preamble,
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
	require.NotNil(t, ts.Assistant.Preamble)
	require.Equal(t, preamble, ts.Assistant.Preamble.Content)
	require.NotNil(t, ts.Assistant.Final)
	require.Equal(t, final, ts.Assistant.Final.Content)
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
