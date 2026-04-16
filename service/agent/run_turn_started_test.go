package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestService_startTurn_UsesParentMessageIDAsStartedByMessageID(t *testing.T) {
	t.Parallel()

	recorder := &recordingConvClient{}
	svc := &Service{conversation: recorder}
	turn := memory.TurnMeta{
		ConversationID:  "conv-1",
		TurnID:          "turn-1",
		ParentMessageID: "user-msg-1",
		Assistant:       "chatter",
	}

	err := svc.startTurn(context.Background(), turn, "")
	require.NoError(t, err)
	require.NotNil(t, recorder.lastTurn)
	require.Equal(t, stringPtrRunTurnStarted("user-msg-1"), recorder.lastTurn.StartedByMessageID)
}

func stringPtrRunTurnStarted(v string) *string { return &v }

var _ apiconv.Client = (*recordingConvClient)(nil)

func TestService_persistInitialUserMessage_AfterTurnExists(t *testing.T) {
	t.Parallel()

	recorder := &starterOrderingConvClient{turnExists: map[string]bool{}}
	svc := &Service{conversation: recorder}
	turn := memory.TurnMeta{
		ConversationID: "conv-1",
		TurnID:         "turn-1",
	}

	err := svc.startTurn(context.Background(), turn, "")
	require.NoError(t, err)

	err = svc.persistInitialUserMessage(context.Background(), &turn, "user-1", "fast cat", "fast cat")
	require.NoError(t, err)
	require.Equal(t, []string{
		"turn:create:turn-1",
		"message:turn-1",
		"turn:starter:turn-1",
	}, recorder.calls)
	require.NotEmpty(t, turn.ParentMessageID)
	require.Equal(t, turn.ParentMessageID, recorder.startedByMessageID)
}

type starterOrderingConvClient struct {
	calls              []string
	turnExists         map[string]bool
	startedByMessageID string
}

func (s *starterOrderingConvClient) GetConversation(context.Context, string, ...apiconv.Option) (*apiconv.Conversation, error) {
	return nil, nil
}

func (s *starterOrderingConvClient) GetConversations(context.Context, *apiconv.Input) ([]*apiconv.Conversation, error) {
	return nil, nil
}

func (s *starterOrderingConvClient) PatchConversations(context.Context, *apiconv.MutableConversation) error {
	return nil
}

func (s *starterOrderingConvClient) GetPayload(context.Context, string) (*apiconv.Payload, error) {
	return nil, nil
}

func (s *starterOrderingConvClient) PatchPayload(context.Context, *apiconv.MutablePayload) error {
	return nil
}

func (s *starterOrderingConvClient) PatchMessage(_ context.Context, message *apiconv.MutableMessage) error {
	turnID := ""
	if message != nil && message.TurnID != nil {
		turnID = *message.TurnID
	}
	if !s.turnExists[turnID] {
		return apiconv.ErrInvalidInput
	}
	s.calls = append(s.calls, "message:"+turnID)
	return nil
}

func (s *starterOrderingConvClient) GetMessage(context.Context, string, ...apiconv.Option) (*apiconv.Message, error) {
	return nil, nil
}

func (s *starterOrderingConvClient) GetMessageByElicitation(context.Context, string, string) (*apiconv.Message, error) {
	return nil, nil
}

func (s *starterOrderingConvClient) PatchModelCall(context.Context, *apiconv.MutableModelCall) error {
	return nil
}

func (s *starterOrderingConvClient) PatchToolCall(context.Context, *apiconv.MutableToolCall) error {
	return nil
}

func (s *starterOrderingConvClient) PatchTurn(_ context.Context, turn *apiconv.MutableTurn) error {
	turnID := ""
	if turn != nil {
		turnID = turn.Id
	}
	if turn != nil && turn.StartedByMessageID != nil {
		s.startedByMessageID = *turn.StartedByMessageID
		s.calls = append(s.calls, "turn:starter:"+turnID)
		return nil
	}
	s.turnExists[turnID] = true
	s.calls = append(s.calls, "turn:create:"+turnID)
	return nil
}

func (s *starterOrderingConvClient) DeleteConversation(context.Context, string) error {
	return nil
}

func (s *starterOrderingConvClient) DeleteMessage(context.Context, string, string) error {
	return nil
}
