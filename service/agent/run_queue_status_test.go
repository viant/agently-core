package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	memconv "github.com/viant/agently-core/internal/service/conversation/memory"
	"github.com/viant/agently-core/runtime/memory"
)

func TestService_PatchQueuedStarterMessageStatus(t *testing.T) {
	testCases := []struct {
		name       string
		inStatus   string
		wantStatus string
	}{
		{name: "failed normalizes to rejected", inStatus: "failed", wantStatus: "rejected"},
		{name: "canceled normalizes to cancel", inStatus: "canceled", wantStatus: "cancel"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := memconv.New()
			svc := &Service{conversation: client}

			conv := apiconv.NewConversation()
			conv.SetId("c1")
			require.NoError(t, client.PatchConversations(ctx, conv))

			turn := apiconv.NewTurn()
			turn.SetId("t1")
			turn.SetConversationID("c1")
			turn.SetStatus("queued")
			require.NoError(t, client.PatchTurn(ctx, turn))

			msg := apiconv.NewMessage()
			msg.SetId("m1")
			msg.SetConversationID("c1")
			msg.SetTurnID("t1")
			msg.SetRole("user")
			msg.SetType("task")
			msg.SetContent("hello")
			require.NoError(t, client.PatchMessage(ctx, msg))

			svc.patchQueuedStarterMessageStatus(ctx, "c1", "t1", "m1", tc.inStatus)

			got, err := client.GetMessage(ctx, "m1")
			require.NoError(t, err)
			require.NotNil(t, got)
			require.NotNil(t, got.Status)
			require.Equal(t, tc.wantStatus, *got.Status)
		})
	}
}

func TestService_FinalizeTurn_UpdatesStarterMessageStatus(t *testing.T) {
	boom := errors.New("boom")
	testCases := []struct {
		name           string
		turnStatus     string
		runErr         error
		initialStatus  string
		wantTurnStatus string
		wantMsgStatus  string
	}{
		{
			name:           "failed turn marks starter message rejected",
			turnStatus:     "failed",
			runErr:         boom,
			initialStatus:  "pending",
			wantTurnStatus: "failed",
			wantMsgStatus:  "rejected",
		},
		{
			name:           "error turn marks starter message rejected",
			turnStatus:     "error",
			runErr:         boom,
			initialStatus:  "pending",
			wantTurnStatus: "error",
			wantMsgStatus:  "rejected",
		},
		{
			name:           "canceled turn marks starter message cancel",
			turnStatus:     "canceled",
			runErr:         context.Canceled,
			initialStatus:  "pending",
			wantTurnStatus: "canceled",
			wantMsgStatus:  "cancel",
		},
		{
			name:           "succeeded turn leaves starter message unchanged",
			turnStatus:     "succeeded",
			initialStatus:  "pending",
			wantTurnStatus: "succeeded",
			wantMsgStatus:  "pending",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := memconv.New()
			svc := &Service{conversation: client}
			seedTurnState(t, ctx, client, "c1", "t1", "m1", tc.initialStatus)

			turn := memory.TurnMeta{
				ConversationID:  "c1",
				TurnID:          "t1",
				ParentMessageID: "m1",
			}
			gotErr := svc.finalizeTurn(ctx, turn, tc.turnStatus, tc.runErr)
			if tc.runErr != nil {
				require.ErrorIs(t, gotErr, tc.runErr)
			} else {
				require.NoError(t, gotErr)
			}

			gotTurn, err := client.GetConversation(ctx, "c1")
			require.NoError(t, err)
			require.NotNil(t, gotTurn)
			require.Len(t, gotTurn.Transcript, 1)
			require.Equal(t, tc.wantTurnStatus, gotTurn.Transcript[0].Status)

			gotMsg, err := client.GetMessage(ctx, "m1")
			require.NoError(t, err)
			require.NotNil(t, gotMsg)
			require.NotNil(t, gotMsg.Status)
			require.Equal(t, tc.wantMsgStatus, *gotMsg.Status)
		})
	}
}

func TestService_RegisterTurnCancel_UpdatesStarterMessageStatus(t *testing.T) {
	ctx := context.Background()
	client := memconv.New()
	svc := &Service{conversation: client}
	seedTurnState(t, ctx, client, "c1", "t1", "m1", "pending")

	turn := memory.TurnMeta{
		ConversationID:  "c1",
		TurnID:          "t1",
		ParentMessageID: "m1",
	}
	_, cancel := svc.registerTurnCancel(ctx, turn)
	cancel()

	gotConv, err := client.GetConversation(ctx, "c1")
	require.NoError(t, err)
	require.NotNil(t, gotConv)
	require.Len(t, gotConv.Transcript, 1)
	require.Equal(t, "canceled", gotConv.Transcript[0].Status)

	gotMsg, err := client.GetMessage(ctx, "m1")
	require.NoError(t, err)
	require.NotNil(t, gotMsg)
	require.NotNil(t, gotMsg.Status)
	require.Equal(t, "cancel", *gotMsg.Status)
}

func seedTurnState(t *testing.T, ctx context.Context, client *memconv.Client, conversationID, turnID, messageID, messageStatus string) {
	t.Helper()

	conv := apiconv.NewConversation()
	conv.SetId(conversationID)
	require.NoError(t, client.PatchConversations(ctx, conv))

	turn := apiconv.NewTurn()
	turn.SetId(turnID)
	turn.SetConversationID(conversationID)
	turn.SetStatus("queued")
	turn.SetStartedByMessageID(messageID)
	require.NoError(t, client.PatchTurn(ctx, turn))

	msg := apiconv.NewMessage()
	msg.SetId(messageID)
	msg.SetConversationID(conversationID)
	msg.SetTurnID(turnID)
	msg.SetRole("user")
	msg.SetType("task")
	msg.SetContent("hello")
	if messageStatus != "" {
		msg.SetStatus(messageStatus)
	}
	require.NoError(t, client.PatchMessage(ctx, msg))
}
