package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/streaming"
	authctx "github.com/viant/agently-core/service/auth"
)

type inlineReportCatalogStub struct {
	values []string
}

func (s inlineReportCatalogStub) InlineReportWorkspaceDataSources(context.Context) ([]string, error) {
	return s.values, nil
}

func TestEnrichStreamingRenderedContent_AttachesCanonicalProgressiveReport(t *testing.T) {
	content := map[string]*streamingRenderedState{}
	messageID := "assistant-1"
	fragments := []string{
		"```forge-data\n{\"version\":2,\"scope\":\"campaign\",\"reportRef\":\"delivery\",\"id\":\"rows\",\"sequence\":1,\"data\":[{\"spend\":12}]}\n```\n",
		"```forge-report\n{\"version\":1,\"scope\":\"campaign\",\"id\":\"delivery\",\"sequence\":2,\"mode\":\"start\",\"blocks\":[{\"id\":\"summary\",\"kind\":\"dashboard.summary\",\"dataSourceRef\":\"rows\"}]}\n```",
	}

	first := enrichStreamingRenderedContent(&streaming.Event{
		Type:               streaming.EventTypeTextDelta,
		AssistantMessageID: messageID,
		Content:            fragments[0],
	}, content)
	require.NotEmpty(t, first.RenderedContent)

	second := enrichStreamingRenderedContent(&streaming.Event{
		Type:               streaming.EventTypeTextDelta,
		AssistantMessageID: messageID,
		Content:            fragments[1],
	}, content)
	require.NotEmpty(t, second.RenderedContent)
	rendered := requireStreamingRenderedContent(t, second)
	require.Len(t, rendered.Reports, 1)
	require.Equal(t, "delivery", rendered.Reports[0].ID)
	require.Equal(t, "rendering", rendered.Reports[0].Status)
	var rows []map[string]interface{}
	require.NoError(t, json.Unmarshal(rendered.Reports[0].DataSources["rows"].Payload, &rows))
	require.Equal(t, float64(12), rows[0]["spend"])
}

func TestEnrichStreamingRenderedContent_DoesNotMutateSourceEvent(t *testing.T) {
	event := &streaming.Event{
		Type:               streaming.EventTypeModelCompleted,
		AssistantMessageID: "assistant-1",
		Content:            "plain text",
	}
	out := enrichStreamingRenderedContent(event, map[string]*streamingRenderedState{})
	require.NotSame(t, event, out)
	require.Nil(t, event.RenderedContent)
}

func TestEnrichStreamingRenderedContent_IsolatesReportAssembliesByAssistantMessage(t *testing.T) {
	states := map[string]*streamingRenderedState{}
	build := func(messageID, title string) *streaming.Event {
		return enrichStreamingRenderedContent(&streaming.Event{
			Type:               streaming.EventTypeModelCompleted,
			AssistantMessageID: messageID,
			Content: "```forge-report\n" +
				fmt.Sprintf(`{"version":1,"scope":"shared","id":"brief","sequence":1,"mode":"start","title":%q,"blocks":[]}`, title) +
				"\n```",
		}, states)
	}

	first := build("assistant-1", "First message")
	second := build("assistant-2", "Second message")
	require.Len(t, states, 2)

	firstContent := requireStreamingRenderedContent(t, first)
	secondContent := requireStreamingRenderedContent(t, second)
	require.Len(t, firstContent.Reports, 1)
	require.Len(t, secondContent.Reports, 1)
	require.Contains(t, string(firstContent.Reports[0].Source), "First message")
	require.NotContains(t, string(firstContent.Reports[0].Source), "Second message")
	require.Contains(t, string(secondContent.Reports[0].Source), "Second message")
	require.NotContains(t, string(secondContent.Reports[0].Source), "First message")
}

func TestEnrichStreamingRenderedContent_EmitsOnlyAtChangedBoundariesAndTerminalEvents(t *testing.T) {
	content := map[string]*streamingRenderedState{}
	messageID := "assistant-1"
	report := "```forge-report\n{\"version\":1,\"id\":\"brief\",\"sequence\":1,\"mode\":\"start\",\"blocks\":[]}\n```"

	boundary := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeTextDelta, AssistantMessageID: messageID, Content: report,
	}, content)
	require.NotEmpty(t, boundary.RenderedContent)

	plain := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeTextDelta, AssistantMessageID: messageID, Content: " trailing narration",
	}, content)
	require.Empty(t, plain.RenderedContent)

	terminal := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeModelCompleted, AssistantMessageID: messageID,
	}, content)
	require.NotEmpty(t, terminal.RenderedContent)
}

func TestEnrichStreamingRenderedContent_DoesNotImplicitlyCommitAtFenceBoundary(t *testing.T) {
	content := map[string]*streamingRenderedState{}
	fragment := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"summary","kind":"dashboard.summary"}]}` +
		"\n```"

	boundary := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeTextDelta, AssistantMessageID: "assistant-1", Content: fragment,
	}, content)
	rendering := requireStreamingRenderedContent(t, boundary)
	require.Equal(t, "rendering", rendering.Reports[0].Status)

	terminal := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeModelCompleted, AssistantMessageID: "assistant-1",
	}, content)
	committed := requireStreamingRenderedContent(t, terminal)
	require.Equal(t, "committed", committed.Reports[0].Status)
}

func TestEnrichStreamingRenderedContent_ImplicitlyCommitsInterruptedTerminalSnapshot(t *testing.T) {
	content := map[string]*streamingRenderedState{}
	fragment := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","blocks":[{"id":"summary","kind":"dashboard.summary"}]}` +
		"\n```"
	enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeTextDelta, AssistantMessageID: "assistant-1", Content: fragment,
	}, content)

	interrupted := enrichStreamingRenderedContent(&streaming.Event{
		Type: streaming.EventTypeTurnCanceled, AssistantMessageID: "assistant-1",
	}, content)
	rendered := requireStreamingRenderedContent(t, interrupted)
	require.Equal(t, "committed", rendered.Reports[0].Status)
}

func TestContainsStreamingFenceBoundary_HandlesSplitDelimiter(t *testing.T) {
	require.True(t, containsStreamingFenceBoundary("prefix ``", "`forge-report"))
	require.True(t, containsStreamingFenceBoundary("prefix `", "``forge-report"))
	require.False(t, containsStreamingFenceBoundary("prefix", " narration"))
}

func TestEnrichStreamingRenderedContent_GatesWorkspaceReferencesFromBackendCatalog(t *testing.T) {
	report := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[]}` +
		"\n```"
	event := &streaming.Event{
		Type: streaming.EventTypeModelCompleted, AssistantMessageID: "assistant-1", Content: report,
	}

	allowed := enrichStreamingRenderedContentWithCatalog(
		authctx.InjectUser(context.Background(), "user-1"), event, map[string]*streamingRenderedState{}, inlineReportCatalogStub{values: []string{"metrics"}},
	)
	allowedContent := requireStreamingRenderedContent(t, allowed)
	require.Len(t, allowedContent.Reports, 1)
	require.Empty(t, allowedContent.Diagnostics)

	denied := enrichStreamingRenderedContentWithCatalog(
		context.Background(), event, map[string]*streamingRenderedState{}, inlineReportCatalogStub{values: []string{"other"}},
	)
	deniedContent := requireStreamingRenderedContent(t, denied)
	require.Empty(t, deniedContent.Reports)
	require.Contains(t, warningCodes(deniedContent.Diagnostics), "REPORT_WORKSPACE_REF_DENIED")
}

func TestEnrichStreamingRenderedContent_DeniesWorkspaceReferenceWithoutEffectiveUser(t *testing.T) {
	report := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[]}` +
		"\n```"
	event := &streaming.Event{Type: streaming.EventTypeModelCompleted, AssistantMessageID: "assistant-1", Content: report}

	result := enrichStreamingRenderedContentWithCatalog(
		context.Background(), event, map[string]*streamingRenderedState{}, inlineReportCatalogStub{values: []string{"metrics"}},
	)
	rendered := requireStreamingRenderedContent(t, result)
	require.Empty(t, rendered.Reports)
	require.Contains(t, warningCodes(rendered.Diagnostics), "REPORT_WORKSPACE_REF_DENIED")
}

func requireStreamingRenderedContent(t *testing.T, event *streaming.Event) RenderedContent {
	t.Helper()
	require.NotNil(t, event)
	require.NotNil(t, event.RenderedContent)
	return *event.RenderedContent
}

func TestApplyInlineReportWorkspaceCatalogToState_GatesHydratedTranscript(t *testing.T) {
	content := "```forge-report\n" +
		`{"version":1,"id":"brief","sequence":1,"mode":"start","datasets":[{"id":"delivery","kind":"workspaceRef","dataSourceRef":"metrics"}],"blocks":[]}` +
		"\n```"
	state := &ConversationState{Turns: []*TurnState{{
		Messages:  []*TurnMessageState{{Role: "assistant", Content: content, RenderedContent: NormalizeRenderedContent(content)}},
		Assistant: &AssistantState{Final: &AssistantMessageState{Content: content, RenderedContent: NormalizeRenderedContent(content)}},
	}}}

	applyInlineReportWorkspaceCatalogToState(context.Background(), state, inlineReportCatalogStub{values: []string{"other"}})

	require.Empty(t, state.Turns[0].Messages[0].RenderedContent.Reports)
	require.Empty(t, state.Turns[0].Assistant.Final.RenderedContent.Reports)
	require.Contains(t, warningCodes(state.Turns[0].Messages[0].RenderedContent.Diagnostics), "REPORT_WORKSPACE_REF_DENIED")
	require.Contains(t, warningCodes(state.Turns[0].Assistant.Final.RenderedContent.Diagnostics), "REPORT_WORKSPACE_REF_DENIED")
}
