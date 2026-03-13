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

func strPtr(value string) *string {
	return &value
}
