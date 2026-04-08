package message

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	agconv "github.com/viant/agently-core/pkg/agently/conversation"
	runtimeprojection "github.com/viant/agently-core/runtime/projection"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestProjectUpdatesProjectionState(t *testing.T) {
	ctx := runtimeprojection.WithState(context.Background())
	svc := New(nil)

	input := &ProjectInput{
		TurnIDs:    []string{"turn-1", "turn-2"},
		MessageIDs: []string{"msg-1"},
		Reason:     "hide superseded context",
	}
	output := &ProjectOutput{}

	err := svc.project(ctx, input, output)
	require.NoError(t, err)
	require.Equal(t, []string{"turn-1", "turn-2"}, output.HiddenTurnIDs)
	require.Equal(t, []string{"msg-1"}, output.HiddenMessageIDs)
	require.Equal(t, "hide superseded context", output.Reason)

	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)
	snapshot := state.Snapshot()
	require.Equal(t, []string{"turn-1", "turn-2"}, snapshot.HiddenTurnIDs)
	require.Equal(t, []string{"msg-1"}, snapshot.HiddenMessageIDs)
	require.Equal(t, "hide superseded context", snapshot.Reason)
}

func TestProjectDedupsAndMerges(t *testing.T) {
	ctx := runtimeprojection.WithState(context.Background())
	state, ok := runtimeprojection.StateFromContext(ctx)
	require.True(t, ok)
	state.HideTurns("turn-1")
	state.HideMessages("msg-1")
	state.SetReason("existing")

	svc := New(nil)
	input := &ProjectInput{
		TurnIDs:    []string{"turn-1", "turn-2"},
		MessageIDs: []string{"msg-1", "msg-2"},
		Reason:     "updated",
	}
	output := &ProjectOutput{}

	err := svc.project(ctx, input, output)
	require.NoError(t, err)
	require.Equal(t, []string{"turn-1", "turn-2"}, output.HiddenTurnIDs)
	require.Equal(t, []string{"msg-1", "msg-2"}, output.HiddenMessageIDs)
	require.Equal(t, "existing; updated", output.Reason)
}

func TestProjectRequiresProjectionState(t *testing.T) {
	svc := New(nil)
	err := svc.project(context.Background(), &ProjectInput{TurnIDs: []string{"turn-1"}}, &ProjectOutput{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "projection state")
}

func TestProjectExpandsTurnIDsToMessageIDs(t *testing.T) {
	ctx := runtimeprojection.WithState(context.Background())
	ctx = memory.WithTurnMeta(ctx, memory.TurnMeta{ConversationID: "conv-1"})
	svc := New(&stubProjectConversationClient{
		conversation: &apiconv.Conversation{
			Transcript: []*agconv.TranscriptView{
				{
					Id: "turn-1",
					Message: []*agconv.MessageView{
						{
							Id:     "msg-1",
							TurnId: strPtrProject("turn-1"),
							ToolMessage: []*agconv.ToolMessageView{
								{Id: "tool-msg-1"},
							},
						},
					},
				},
			},
		},
	})
	out := &ProjectOutput{}
	err := svc.project(ctx, &ProjectInput{TurnIDs: []string{"turn-1"}}, out)
	require.NoError(t, err)
	require.Equal(t, []string{"turn-1"}, out.HiddenTurnIDs)
	require.Contains(t, out.HiddenMessageIDs, "msg-1")
	require.Contains(t, out.HiddenMessageIDs, "tool-msg-1")
}

type stubProjectConversationClient struct {
	conversation *apiconv.Conversation
}

func (s *stubProjectConversationClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}
func (s *stubProjectConversationClient) PatchMessage(context.Context, *apiconv.MutableMessage) error {
	return nil
}
func (s *stubProjectConversationClient) PatchTurn(context.Context, *apiconv.MutableTurn) error {
	return nil
}
func (s *stubProjectConversationClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}
func (s *stubProjectConversationClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}
func (s *stubProjectConversationClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return s.conversation, nil
}
func (s *stubProjectConversationClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}
func (s *stubProjectConversationClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}
func (s *stubProjectConversationClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}
func (s *stubProjectConversationClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}
func (s *stubProjectConversationClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}
func (s *stubProjectConversationClient) DeleteConversation(context.Context, string) error {
	return nil
}
func (s *stubProjectConversationClient) DeleteMessage(context.Context, string, string) error {
	return nil
}

func strPtrProject(v string) *string { return &v }
