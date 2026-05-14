package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

func TestTranscriptHistory_EmptyTranscript(t *testing.T) {
	var tr Transcript

	require.Nil(t, (&tr).History(false))
	require.Nil(t, (&tr).History(true))
}

func TestTranscriptHistory_NilTranscript(t *testing.T) {
	var tr *Transcript

	require.Nil(t, tr.History(false))
	require.Nil(t, tr.History(true))
}

func TestTranscript_LastAssistantMessageWithModelCall_SkipsSummaryMode(t *testing.T) {
	now := time.Now().UTC()

	respMain := "resp-main"
	respSummary := "resp-summary"
	payloadMain := "payload-main"
	payloadSummary := "payload-summary"
	modePlan := "plan"
	modeSummary := "summary"

	main := &Message{
		Id:        "msg-main",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now.Add(-1 * time.Second),
		Mode:      &modePlan,
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadMain, TraceId: &respMain},
	}

	summary := &Message{
		Id:        "msg-summary",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		Mode:      &modeSummary,
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadSummary, TraceId: &respSummary},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(main), (*agconv.MessageView)(summary)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, respMain, *got.ModelCall.TraceId)
}

func TestTranscript_LastAssistantMessageWithModelCall_SkipsRouterMode(t *testing.T) {
	now := time.Now().UTC()

	respMain := "resp-main"
	respRouter := "resp-router"
	payloadMain := "payload-main"
	payloadRouter := "payload-router"
	modeRouter := "router"

	main := &Message{
		Id:        "msg-main",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now.Add(-1 * time.Second),
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadMain, TraceId: &respMain},
	}

	router := &Message{
		Id:        "msg-router",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		Mode:      &modeRouter,
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadRouter, TraceId: &respRouter},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(main), (*agconv.MessageView)(router)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, respMain, *got.ModelCall.TraceId)
}

func TestTranscript_LastAssistantMessageWithModelCall_SkipsSummaryStatus(t *testing.T) {
	now := time.Now().UTC()

	respMain := "resp-main"
	respSummary := "resp-summary"
	payloadMain := "payload-main"
	payloadSummary := "payload-summary"
	statusSummary := "summary"

	main := &Message{
		Id:        "msg-main",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now.Add(-1 * time.Second),
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadMain, TraceId: &respMain},
	}

	summary := &Message{
		Id:        "msg-summary",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		Status:    &statusSummary,
		ModelCall: &agconv.ModelCallView{ProviderResponsePayloadId: &payloadSummary, TraceId: &respSummary},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(main), (*agconv.MessageView)(summary)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, respMain, *got.ModelCall.TraceId)
}

func TestTranscript_LastAssistantMessageWithModelCall_AllowsTraceOnlyModelCall(t *testing.T) {
	now := time.Now().UTC()
	resp := "resp-trace-only"

	traceOnly := &Message{
		Id:        "msg-trace-only",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		ModelCall: &agconv.ModelCallView{TraceId: &resp},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(traceOnly)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, resp, *got.ModelCall.TraceId)
}

func TestTranscript_LastAssistantMessageWithModelCall_SkipsNullChoiceResponsePayload(t *testing.T) {
	now := time.Now().UTC()

	respGood := "resp-good"
	respBad := "resp-bad"
	content := "Working through the blocker proof."
	nullChoices := `{"choices":null,"response_id":"resp-bad"}`
	toolResponse := `{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"call-1","name":"llm_agents-start"}]}}]}`

	good := &Message{
		Id:        "msg-good",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now.Add(-1 * time.Second),
		Content:   &content,
		ModelCall: &agconv.ModelCallView{
			TraceId: &respGood,
			ModelCallResponsePayload: &agconv.ModelCallStreamPayloadView{
				InlineBody: &toolResponse,
			},
		},
	}

	bad := &Message{
		Id:        "msg-bad",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		ModelCall: &agconv.ModelCallView{
			TraceId: &respBad,
			ModelCallResponsePayload: &agconv.ModelCallStreamPayloadView{
				InlineBody: &nullChoices,
			},
		},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(good), (*agconv.MessageView)(bad)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.Equal(t, "msg-good", got.Id)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, respGood, *got.ModelCall.TraceId)
}

func TestTranscript_LastAssistantMessageWithModelCall_AllowsBlankToolOnlyAnchorWithToolChild(t *testing.T) {
	now := time.Now().UTC()

	respEarly := "resp-early"
	respLate := "resp-late"
	earlyContent := "Starting baseline."
	nullChoices := `{"choices":null,"response_id":"resp-late"}`

	early := &Message{
		Id:        "msg-early",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now.Add(-2 * time.Second),
		Content:   &earlyContent,
		ModelCall: &agconv.ModelCallView{TraceId: &respEarly},
	}

	lateToolChild := &agconv.ToolMessageView{
		Id:        "tool-msg-late",
		CreatedAt: now.Add(-1 * time.Second),
		Type:      "tool_op",
		Content:   strPtr(`{"conversationId":"child-1","status":"running"}`),
		ToolCall: &agconv.ToolCallView{
			ToolName: "llm/agents:start",
			TraceId:  &respLate,
		},
	}
	late := &Message{
		Id:        "msg-late",
		Role:      "assistant",
		Type:      "text",
		CreatedAt: now,
		Interim:   1,
		ModelCall: &agconv.ModelCallView{
			TraceId: &respLate,
			ModelCallResponsePayload: &agconv.ModelCallStreamPayloadView{
				InlineBody: &nullChoices,
			},
		},
		ToolMessage: []*agconv.ToolMessageView{lateToolChild},
	}

	turn := &Turn{Id: "turn-1", Message: []*agconv.MessageView{(*agconv.MessageView)(early), (*agconv.MessageView)(late)}}
	tr := Transcript{turn}

	got := (&tr).LastAssistantMessageWithModelCall()
	require.NotNil(t, got)
	require.Equal(t, "msg-late", got.Id)
	require.NotNil(t, got.ModelCall)
	require.NotNil(t, got.ModelCall.TraceId)
	require.Equal(t, respLate, *got.ModelCall.TraceId)
}

func strPtr(v string) *string { return &v }
