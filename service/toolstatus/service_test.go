package toolstatus

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
