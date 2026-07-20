package sdk

import (
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
	"testing"
)

func TestNormalizeRenderedContent_ProducesOrderedForgeParts(t *testing.T) {
	content := "Intro\n```forge-data\n{\"id\":\"rows\",\"format\":\"json\",\"mode\":\"append\",\"data\":[{\"id\":1}]}\n```\n```forge-ui\n{\"version\":1,\"blocks\":[]}\n```\nDone"
	got := NormalizeRenderedContent(content)
	require.NotNil(t, got)
	require.Equal(t, "1", got.SchemaVersion)
	require.Len(t, got.Parts, 5)
	require.Equal(t, "markdown", got.Parts[0].Kind)
	require.Equal(t, "forgeData", got.Parts[1].Kind)
	require.Equal(t, "rows", got.Parts[1].Data.ID)
	require.Equal(t, "append", got.Parts[1].Data.Mode)
	require.Equal(t, "markdown", got.Parts[2].Kind)
	require.Equal(t, "forgeUI", got.Parts[3].Kind)
	require.Equal(t, "markdown", got.Parts[4].Kind)
}

func TestNormalizeRenderedContent_LeavesMalformedOrOpenFencesAsRawContent(t *testing.T) {
	invalid := NormalizeRenderedContent("```forge-ui\ninvalid\n```")
	require.NotNil(t, invalid)
	require.Len(t, invalid.Diagnostics, 1)
	require.Nil(t, NormalizeRenderedContent("```forge-ui\n{\"blocks\":[]}"))
}

func TestNormalizeRenderedContent_RecognizesLegacyForgeDataHeader(t *testing.T) {
	content := "```forge-data id=\"summary_metrics\" format=json mode=append\n[{\"label\":\"Spend\"}]\n```\n```forge-ui\n{\"blocks\":[]}\n```"
	got := NormalizeRenderedContent(content)

	require.NotNil(t, got)
	require.Len(t, got.Parts, 3)
	require.Equal(t, "forgeData", got.Parts[0].Kind)
	require.Equal(t, "summary_metrics", got.Parts[0].Data.ID)
	require.Equal(t, "append", got.Parts[0].Data.Mode)
	require.Equal(t, "forgeUI", got.Parts[2].Kind)
}

func TestNormalizeRenderedContent_RecognizesLegacyCSVForgeDataHeader(t *testing.T) {
	content := "```forge-data id=rows format=csv\nname,spend\none,12.5\n```\n```forge-ui\n{\"blocks\":[]}\n```"
	got := NormalizeRenderedContent(content)

	require.NotNil(t, got)
	require.Len(t, got.Parts, 3)
	require.Equal(t, "forgeData", got.Parts[0].Kind)
	require.Equal(t, "rows", got.Parts[0].Data.ID)
	require.Equal(t, "csv", got.Parts[0].Data.Format)
	require.JSONEq(t, `"name,spend\none,12.5\n"`, string(got.Parts[0].Data.Payload))
	require.Equal(t, "forgeUI", got.Parts[2].Kind)
}

func TestNormalizeRenderedContent_DoesNotCloseOnBackticksInsideJSON(t *testing.T) {
	content := "```forge-ui\n{\"blocks\":[{\"kind\":\"dashboard.report\",\"body\":\"Use ```code``` here\"}]}\n```"
	got := NormalizeRenderedContent(content)

	require.NotNil(t, got)
	require.Len(t, got.Parts, 1)
	require.Equal(t, "forgeUI", got.Parts[0].Kind)
}

func TestReduceHydratesRenderedContentForLiveAssistantPage(t *testing.T) {
	content := "```forge-data id=\"summary_metrics\"\n[{\"label\":\"Spend\"}]\n```\n```forge-ui\n{\"blocks\":[]}\n```"
	state := Reduce(nil, &streaming.Event{
		Type:           streaming.EventTypeTurnStarted,
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	state = Reduce(state, &streaming.Event{
		Type:               streaming.EventTypeModelCompleted,
		ConversationID:     "conv-1",
		TurnID:             "turn-1",
		AssistantMessageID: "assistant-1",
		Content:            content,
	})

	page := state.Turns[0].Execution.Pages[0]
	require.NotNil(t, page.RenderedContent)
	require.Equal(t, "summary_metrics", page.RenderedContent.Parts[0].Data.ID)
}
