package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

type addFakeConv struct {
	fakeConv
	patchedMessages []*apiconv.MutableMessage
	patchedConvs    []*apiconv.MutableConversation
}

func (f *addFakeConv) PatchMessage(_ context.Context, msg *apiconv.MutableMessage) error {
	if msg.Sequence == nil {
		msg.SetSequence(17)
	}
	f.patchedMessages = append(f.patchedMessages, msg)
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
