package message

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type addFakeConv struct {
	fakeConv
	patchedMessages []*apiconv.MutableMessage
	patchedConvs    []*apiconv.MutableConversation
	addEventFlags   []bool
}

func (f *addFakeConv) PatchMessage(ctx context.Context, msg *apiconv.MutableMessage) error {
	if msg.Sequence == nil {
		msg.SetSequence(17)
	}
	f.patchedMessages = append(f.patchedMessages, msg)
	f.addEventFlags = append(f.addEventFlags, memory.MessageAddEventFromContext(ctx))
	return nil
}

func (f *addFakeConv) PatchConversations(_ context.Context, conv *apiconv.MutableConversation) error {
	f.patchedConvs = append(f.patchedConvs, conv)
	return nil
}

func TestAdd_UsesTurnParentMessageForStandaloneAssistantNote(t *testing.T) {
	conv := &addFakeConv{}
	svc := New(conv)
	turn := memory.TurnMeta{
		ConversationID:  "conv-1",
		TurnID:          "turn-1",
		ParentMessageID: "parent-from-turn",
	}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	ctx = memory.WithModelMessageID(ctx, "assistant-msg-1")

	input := &AddInput{
		Role:    "assistant",
		Content: "Preliminary investigation: PMP supply looks concentrated.",
		Mode:    "task",
	}
	var out AddOutput
	err := svc.add(ctx, input, &out)
	require.NoError(t, err)
	require.Len(t, conv.patchedMessages, 1)
	msg := conv.patchedMessages[0]
	require.Equal(t, "assistant", msg.Role)
	require.Equal(t, "conv-1", msg.ConversationID)
	require.Equal(t, "turn-1", deref(msg.TurnID))
	require.Equal(t, "parent-from-turn", deref(msg.ParentMessageID))
	require.Equal(t, "task", deref(msg.Mode))
	require.Equal(t, "Preliminary investigation: PMP supply looks concentrated.", deref(msg.Content))
	require.Equal(t, "parent-from-turn", out.ParentMessageID)
	require.Equal(t, 17, out.Sequence)
	require.Equal(t, []bool{true}, conv.addEventFlags)
}

func TestAdd_UsesModelParentForInterimNoteWhenTurnParentMissing(t *testing.T) {
	conv := &addFakeConv{}
	svc := New(conv)
	turn := memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	}
	ctx := memory.WithTurnMeta(context.Background(), turn)
	ctx = memory.WithModelMessageID(ctx, "assistant-msg-1")
	isInterim := true

	input := &AddInput{
		Role:    "assistant",
		Content: "Streaming note",
		Mode:    "task",
		Interim: &isInterim,
	}
	var out AddOutput
	err := svc.add(ctx, input, &out)
	require.NoError(t, err)
	require.Len(t, conv.patchedMessages, 1)
	msg := conv.patchedMessages[0]
	require.Equal(t, "assistant-msg-1", deref(msg.ParentMessageID))
	require.Equal(t, "assistant-msg-1", out.ParentMessageID)
	require.Equal(t, []bool{false}, conv.addEventFlags)
}

func TestAdd_RejectsNonAssistantRole(t *testing.T) {
	conv := &addFakeConv{}
	svc := New(conv)
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	})
	var out AddOutput
	err := svc.add(ctx, &AddInput{
		Role:    "user",
		Content: "not allowed",
	}, &out)
	require.Error(t, err)
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func TestAdd_RejectsMalformedReportBeforeWritesAndAcceptsCorrectedRetry(t *testing.T) {
	valid := "```forge-report\n" + `{"version":1,"id":"example","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"finding","kind":"markdownBlock","markdown":"The rule is [[\"if\",6]].\nThe value is [Value]."}]}` + "\n```"
	cases := map[string]string{
		"quoted rule":             strings.Replace(valid, `\"if\"`, `"if"`, 1),
		"literal newline":         strings.Replace(valid, `\nThe value`, "\nThe value", 1),
		"unclosed report":         strings.TrimSuffix(valid, "\n```"),
		"malformed attached data": valid + "\n```forge-data\n" + `{"version":2,"id":"rows","reportRef":"example","sequence":2,"data":[{"value":"[Value]"}]` + "\n```",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			for _, interim := range []bool{false, true} {
				conv := &addFakeConv{}
				svc := New(conv)
				ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "example", TurnID: "turn"})
				var output AddOutput
				err := svc.add(ctx, &AddInput{Content: content, Interim: &interim}, &output)
				require.ErrorContains(t, err, "report was not saved")
				require.ErrorContains(t, err, "retry message:add")
				require.Empty(t, conv.patchedMessages)
				require.Empty(t, conv.patchedConvs)
				require.Empty(t, output.MessageID)
				require.NoError(t, svc.add(ctx, &AddInput{Content: valid, Interim: &interim}, &output))
				require.Len(t, conv.patchedMessages, 1)
				require.Equal(t, valid, deref(conv.patchedMessages[0].Content))
				require.NotEmpty(t, output.MessageID)
			}
		})
	}
}

func TestAdd_PreservesOrdinaryLegacyAndValidReportMessages(t *testing.T) {
	start := "```forge-report\n" + `{"version":1,"id":"example","sequence":1,"mode":"start","grammar":"report-document-v1","blocks":[{"id":"summary","kind":"markdownBlock","markdown":"Example."}]}` + "\n```"
	for _, content := range []string{
		"An ordinary note mentioning forge-report.",
		"```forge-ui\n" + `{"kind":"dashboard.guidedResult","title":"Example"}` + "\n```",
		"```forge-data id=rows format=csv\nlabel,value\nExample,1\n```\n```forge-ui\n{}\n```",
		start,
		start + "\n```forge-report\n" + `{"version":1,"id":"example","sequence":2,"mode":"commit"}` + "\n```",
	} {
		conv := &addFakeConv{}
		svc := New(conv)
		ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "example", TurnID: "turn"})
		require.NoError(t, svc.add(ctx, &AddInput{Content: content}, &AddOutput{}))
		require.Len(t, conv.patchedMessages, 1)
		require.Equal(t, content, deref(conv.patchedMessages[0].Content))
	}
}
