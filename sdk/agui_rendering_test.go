package sdk

import (
	"context"
	"encoding/json"
	convstore "github.com/viant/agently-core/app/store/conversation"
	conversationmodel "github.com/viant/agently-core/model/conversation"
	"github.com/viant/agently-core/protocol/agui"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/runtime/aguistate"
	"github.com/viant/agently-core/runtime/streaming"
)

func TestAGUIForgePayloadNeverBecomesChatDuringFragmentedStreaming(t *testing.T) {
	content := "Report:\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"secret_authoring_key\":1}]}\n```\n```forge-ui\n{\"version\":1,\"blocks\":[]}\n```\nDone."
	p := &aguiPresentation{}
	var text strings.Builder
	rich := false
	for _, r := range content {
		for _, event := range p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "t", TurnID: "r", MessageID: "m", Content: string(r)}, nil) {
			require.Equal(t, streaming.EventTypeTextDelta, event.Type)
			text.WriteString(event.Content)
			rich = rich || event.RenderedContent != nil
			require.NotContains(t, text.String(), "secret_authoring_key")
			require.NotContains(t, text.String(), "forge-data")
			require.NotContains(t, text.String(), "forge-ui")
		}
	}
	terminal := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: "t", TurnID: "r", MessageID: "m", Content: content}, nil)
	for _, event := range terminal {
		rich = rich || event.RenderedContent != nil
		require.NotContains(t, event.Content, "secret_authoring_key")
	}
	require.True(t, rich)
	require.Equal(t, 1, strings.Count(text.String(), aguiInteractiveFallback))
	require.Contains(t, text.String(), "Report:")
	require.Contains(t, text.String(), "Done.")
}

func TestAGUIPresentationPreservesOrdinaryCodeAndHidesMalformedRichPayload(t *testing.T) {
	ordinary := "Here is JSON:\n```json\n{\"value\":1}\n```\nUse `x`."
	text, rich := plainAGUIContent(ordinary, true)
	require.False(t, rich)
	require.Equal(t, ordinary, text)
	text, rich = plainAGUIContent("```forge-report\n{broken private authoring", true)
	require.True(t, rich)
	require.Equal(t, aguiInteractiveFallback, text)
	text, rich = plainAGUIContent("before\n```for", false)
	require.False(t, rich)
	require.Equal(t, "before\n", text)
}

func TestAGUIPresentationNarrationNeverCarriesForgeAuthoringIntoCommentary(t *testing.T) {
	content := "Working\n```forge-data\n{\"private_authoring\":true}\n```\nDone"
	projected := (&aguiPresentation{}).project(context.Background(), &streaming.Event{Type: streaming.EventTypeNarration, Content: content, Narration: content, NarrationSource: "narrator"}, nil)
	require.Len(t, projected, 1)
	require.NotContains(t, projected[0].Content, "private_authoring")
	require.NotContains(t, projected[0].Narration, "forge-data")
	require.Contains(t, projected[0].Content, "Working")
	require.Contains(t, projected[0].Content, "Done")
	require.Equal(t, "narrator", projected[0].NarrationSource)
}

func TestAGUIPresentationDeduplicatesRawOffsets(t *testing.T) {
	p := &aguiPresentation{}
	zero := 0
	first := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "m", Content: "Hello", ContentOffset: &zero}, nil)
	require.Equal(t, "Hello", first[0].Content)
	replay := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, MessageID: "m", Content: "Hello", ContentOffset: &zero}, nil)
	require.Empty(t, replay[0].Content)
}

func TestAGUIRecoveryPresentationPrefillsRawOffsetsAndNeverLeaksUnknownPrefix(t *testing.T) {
	prefix := "Intro\n```forge-data\n{\"id\":\"rows\",\"data\":[{\"secret_authoring_key\":"
	visible, _ := plainAGUIContent(prefix, false)
	projected := []aguistate.Object{{"id": "m", "role": "assistant", "content": visible}}
	native := []aguistate.Object{{"id": "m", "role": "assistant", "content": prefix}}
	p := &aguiPresentation{}
	p.prefill("t", "turn", "", projected, native)
	offset := len(prefix)
	suffix := "1}]}\n```\nDone"
	var text strings.Builder
	for _, event := range p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "t", TurnID: "turn", MessageID: "m", Content: suffix, ContentOffset: &offset}, nil) {
		text.WriteString(event.Content)
		require.NotContains(t, event.Content, "secret_authoring_key")
		require.NotContains(t, event.Content, "forge-data")
	}
	require.Equal(t, "\nDone", text.String())
	missing := &aguiPresentation{}
	missing.prefill("t", "turn", "", projected, nil)
	require.Empty(t, missing.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "t", TurnID: "turn", MessageID: "m", Content: suffix, ContentOffset: &offset}, nil), "unknown raw prefix cannot pass through as chat")
	snapshot := missing.project(context.Background(), &streaming.Event{Type: streaming.EventTypeAssistant, ConversationID: "t", TurnID: "turn", MessageID: "m", Content: prefix + suffix}, nil)
	require.NotEmpty(t, snapshot)
	require.NotContains(t, snapshot[0].Content, "secret_authoring_key")
	require.Contains(t, snapshot[0].Content, "Done")
}

func TestAGUIPresentationInternalModesDoNotPublishBodies(t *testing.T) {
	for _, mode := range []string{"router", "chain"} {
		p := &aguiPresentation{}
		start := &streaming.Event{Type: streaming.EventTypeModelStarted, ConversationID: "thread", TurnID: "turn", MessageID: "internal", Mode: mode}
		require.Len(t, p.project(context.Background(), start, nil), 1, "model lifecycle retained")
		require.Empty(t, p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "internal", Content: `{"classification":true}`}, nil), "known identity stays internal without repeated mode")
		complete := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: "thread", TurnID: "turn", MessageID: "internal", Mode: mode, Content: "internal prose"}, nil)
		require.Len(t, complete, 1)
		require.Empty(t, complete[0].Content)
		visible := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "task", Mode: "task", Content: `{"classification":true}`}, nil)
		require.Len(t, visible, 1)
		require.Equal(t, `{"classification":true}`, visible[0].Content)
	}
}

func TestAGUIRecognizedPartialReportPublishesTypedProgressWithoutAuthorJSON(t *testing.T) {
	p := &aguiPresentation{}
	events := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "report", Content: "```forge-report\n{\"privateAuthoring\":", ContentMode: "snapshot"}, nil)
	require.NotEmpty(t, events)
	found := false
	for _, event := range events {
		require.NotContains(t, event.Content, "privateAuthoring")
		if event.RenderedContent != nil {
			require.Len(t, event.RenderedContent.Reports, 1)
			require.Equal(t, "rendering", event.RenderedContent.Reports[0].Status)
			require.Nil(t, event.RenderedContent.Reports[0].Source)
			found = true
		}
	}
	require.True(t, found, "recognized report should have typed progress before completion")
}

func TestAGUIProjectedModelSnapshotCannotBecomeStandaloneWithoutModelIDs(t *testing.T) {
	p := &aguiPresentation{}
	p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "model", Content: "Old visible prefix", ContentMode: "snapshot"}, nil)
	events := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "model", Content: "Different visible prefix", ContentMode: "snapshot"}, nil)
	require.Len(t, events, 1)
	require.Equal(t, streaming.EventTypeAssistant, events[0].Type)
	require.Equal(t, true, events[0].Patch["agentlyProjectionSnapshot"])
}

func TestExportOwnedPublicHighlightsReportProducerEvents(t *testing.T) {
	if os.Getenv("AGENTLY_PUBLIC_STREAM_FIXTURE") == "" {
		t.Skip("fixture export not requested")
	}
	tr := agui.NewTranslator("owned-stream", "owned-run")
	p := &aguiPresentation{}
	phases := map[string][]agui.Event{}
	emit := func(phase string, event *streaming.Event) {
		for _, visible := range p.project(context.Background(), event, nil) {
			phases[phase] = append(phases[phase], tr.Translate(visible)...)
		}
	}
	emit("highlights", &streaming.Event{Type: streaming.EventTypeTurnStarted, ConversationID: "owned-stream", TurnID: "owned-turn", Status: "running"})
	emit("highlights", &streaming.Event{Type: streaming.EventTypeAssistant, ConversationID: "owned-stream", TurnID: "owned-turn", MessageID: "owned-highlights", Mode: "task", CreatedAt: time.Date(2026, 10, 9, 12, 0, 1, 0, time.UTC), Content: "Owned textual highlights arrive before the dashboard.", Patch: map[string]any{"role": "assistant"}})
	emit("pending", &streaming.Event{Type: streaming.EventTypeNarration, ConversationID: "owned-stream", TurnID: "owned-turn", MessageID: "owned-progress", Mode: "chain", NarrationSource: "executor", Content: "Preparing owned report data", Narration: "Preparing owned report data", Status: "running"})
	emit("pending", &streaming.Event{Type: streaming.EventTypeModelStarted, ConversationID: "owned-stream", TurnID: "owned-turn", AssistantMessageID: "owned-report", PageID: "report-page", ModelCallID: "owned-model", Mode: "task", Status: "running"})
	emit("pending", &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "owned-stream", TurnID: "owned-turn", MessageID: "owned-report", PageID: "report-page", Mode: "task", CreatedAt: time.Date(2026, 10, 9, 12, 0, 2, 0, time.UTC), Content: "```forge-report\n{", ContentMode: "snapshot"})
	complete := "```forge-report\n" + `{"version":1,"scope":"message","id":"owned-dashboard","sequence":1,"mode":"start","grammar":"report-document-v1","title":"Owned Dashboard","blocks":[{"id":"summary","kind":"markdownBlock","markdown":"Owned result: 12"}]}` + "\n```\n```forge-report\n" + `{"version":1,"scope":"message","id":"owned-dashboard","sequence":2,"mode":"commit"}` + "\n```"
	emit("complete", &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "owned-stream", TurnID: "owned-turn", MessageID: "owned-report", PageID: "report-page", Mode: "task", CreatedAt: time.Date(2026, 10, 9, 12, 0, 2, 0, time.UTC), Content: complete, ContentMode: "snapshot"})
	emit("complete", &streaming.Event{Type: streaming.EventTypeItemCompleted, ConversationID: "owned-stream", TurnID: "owned-turn", MessageID: "owned-report", PageID: "report-page", Mode: "task", CreatedAt: time.Date(2026, 10, 9, 12, 0, 2, 0, time.UTC), Content: complete})
	emit("complete", &streaming.Event{Type: streaming.EventTypeModelCompleted, ConversationID: "owned-stream", TurnID: "owned-turn", AssistantMessageID: "owned-report", PageID: "report-page", ModelCallID: "owned-model", Mode: "task", Status: "completed"})
	emit("complete", &streaming.Event{Type: streaming.EventTypeTurnCompleted, ConversationID: "owned-stream", TurnID: "owned-turn", Status: "completed"})
	raw, err := json.Marshal(phases)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(os.Getenv("AGENTLY_PUBLIC_STREAM_FIXTURE"), raw, 0600))
}

func TestCanonicalHistoryRetainsPublicMessageAddBeforeFinalReport(t *testing.T) {
	noteID := "78d5cfea-5646-4352-896f-722494256c24"
	reportID := "45bd6ebd-6f21-40e2-a500-bc68ee74a07f"
	mode := "task"
	note := "Owned public highlights"
	report := "```forge-report\n" + `{"version":1,"scope":"message","id":"owned","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"summary","kind":"markdownBlock","markdown":"Owned report"}]}` + "\n```\n```forge-report\n" + `{"version":1,"scope":"message","id":"owned","sequence":2,"mode":"commit"}` + "\n```"
	turn := &convstore.Turn{Id: "owned-turn", Status: "succeeded", Message: []*conversationmodel.MessageView{{Id: noteID, Role: "assistant", Mode: &mode, Content: &note, CreatedAt: time.Date(2026, 10, 9, 12, 0, 1, 0, time.UTC)}, {Id: reportID, Role: "assistant", Mode: &mode, Content: &report, CreatedAt: time.Date(2026, 10, 9, 12, 0, 2, 0, time.UTC)}}}
	state := BuildCanonicalState("owned", convstore.Transcript{turn})
	require.Len(t, state.Turns, 1)
	found := false
	for _, message := range state.Turns[0].Messages {
		if message.MessageID == noteID {
			require.Equal(t, note, message.Content)
			require.Equal(t, "task", message.Mode)
			found = true
		}
	}
	require.True(t, found, "public message/add note survives persisted canonical history")
	require.Equal(t, reportID, state.Turns[0].Assistant.Final.MessageID)
}

func TestOperationalInternalNarrationIsSafeStatusWhileReasoningStaysHidden(t *testing.T) {
	p := &aguiPresentation{}
	outputs := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeNarration, ConversationID: "thread", TurnID: "turn", MessageID: "status", Mode: "chain", NarrationSource: "executor", Content: "Preparing report data", Narration: "Preparing report data"}, nil)
	require.Len(t, outputs, 1)
	require.Equal(t, "Preparing report data", outputs[0].Narration)
	hidden := p.project(context.Background(), &streaming.Event{Type: streaming.EventTypeTextDelta, ConversationID: "thread", TurnID: "turn", MessageID: "reasoning", Mode: "chain", Content: "private reasoning"}, nil)
	require.Empty(t, hidden)
}
