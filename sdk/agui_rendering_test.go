package sdk

import (
	"context"
	"strings"
	"testing"

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
