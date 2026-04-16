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
