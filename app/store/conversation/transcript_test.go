package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
)

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
