package status

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	runtimerequestctx "github.com/viant/agently-core/runtime/requestctx"
)

type recordingConv struct {
	patched []*apiconv.MutableMessage
}

func (r *recordingConv) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return nil, nil
}
func (r *recordingConv) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (r *recordingConv) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (r *recordingConv) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (r *recordingConv) PatchPayload(context.Context, *apiconv.MutablePayload) error { return nil }
func (r *recordingConv) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	r.patched = append(r.patched, message)
	return nil
}
func (r *recordingConv) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (r *recordingConv) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (r *recordingConv) PatchModelCall(context.Context, *apiconv.MutableModelCall) error { return nil }
func (r *recordingConv) PatchToolCall(context.Context, *apiconv.MutableToolCall) error   { return nil }
func (r *recordingConv) PatchTurn(context.Context, *apiconv.MutableTurn) error           { return nil }
func (r *recordingConv) DeleteConversation(context.Context, string) error                { return nil }
func (r *recordingConv) DeleteMessage(context.Context, string, string) error             { return nil }

func TestFinalize_SkipsEmptyPreview(t *testing.T) {
	conv := &recordingConv{}
	svc := New(conv)
	parent := runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	err := svc.Finalize(context.Background(), parent, "msg-1", "completed", "")
	require.NoError(t, err)
	require.Empty(t, conv.patched)
}

func TestStartPreamble_CreatesInterimAssistantMessage(t *testing.T) {
	conv := &recordingConv{}
	svc := New(conv)
	parent := runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	msgID, err := svc.StartPreamble(context.Background(), parent, "llm/agents:status", "", "", "", "Working on it")
	require.NoError(t, err)
	require.NotEmpty(t, msgID)
	require.NotEmpty(t, conv.patched)

	last := conv.patched[len(conv.patched)-1]
	require.Equal(t, "assistant", last.Role)
	require.NotNil(t, last.Interim)
	require.EqualValues(t, 1, *last.Interim)
	require.NotNil(t, last.Content)
	require.Equal(t, "Working on it", *last.Content)
	require.NotNil(t, last.Preamble)
	require.Equal(t, "Working on it", *last.Preamble)
}

func TestUpdatePreamble_RefreshesSameMessageID(t *testing.T) {
	conv := &recordingConv{}
	svc := New(conv)
	parent := runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	err := svc.UpdatePreamble(context.Background(), parent, "msg-1", "Still working")
	require.NoError(t, err)
	require.Len(t, conv.patched, 1)

	last := conv.patched[0]
	require.Equal(t, "msg-1", last.Id)
	require.NotNil(t, last.Interim)
	require.EqualValues(t, 1, *last.Interim)
	require.NotNil(t, last.Content)
	require.Equal(t, "Still working", *last.Content)
	require.NotNil(t, last.Preamble)
	require.Equal(t, "Still working", *last.Preamble)
}

func TestPreamblePairing_UpsertReusesMessageIDAndReleaseClearsMapping(t *testing.T) {
	conv := &recordingConv{}
	svc := New(conv)
	pairing := NewPreamblePairing(svc)
	parent := runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	firstID, err := pairing.Upsert(context.Background(), "tool-call-1", parent, "llm/agents:status", "assistant", "tool", "exec", "phase 1")
	require.NoError(t, err)
	require.NotEmpty(t, firstID)

	secondID, err := pairing.Upsert(context.Background(), "tool-call-1", parent, "llm/agents:status", "assistant", "tool", "exec", "phase 2")
	require.NoError(t, err)
	require.Equal(t, firstID, secondID)
	require.Equal(t, firstID, pairing.MessageID("tool-call-1"))

	pairing.Release("tool-call-1")
	require.Equal(t, "", pairing.MessageID("tool-call-1"))
}

func TestPublishFinal_CreatesFinalLinkedAssistantMessage(t *testing.T) {
	conv := &recordingConv{}
	svc := New(conv)
	parent := runtimerequestctx.TurnMeta{ConversationID: "conv-1", TurnID: "turn-1"}

	msgID, err := svc.PublishFinal(context.Background(), parent, "llm/agents:start", "assistant", "tool", "exec", "child-conv", "succeeded", "Detached child completed.")
	require.NoError(t, err)
	require.NotEmpty(t, msgID)
	require.Len(t, conv.patched, 1)

	last := conv.patched[0]
	require.Equal(t, msgID, last.Id)
	require.Equal(t, "assistant", last.Role)
	require.NotNil(t, last.Interim)
	require.EqualValues(t, 0, *last.Interim)
	require.NotNil(t, last.Content)
	require.Equal(t, "Detached child completed.", *last.Content)
	require.NotNil(t, last.LinkedConversationID)
	require.Equal(t, "child-conv", *last.LinkedConversationID)
	require.NotNil(t, last.Status)
	require.Equal(t, "completed", *last.Status)
}
