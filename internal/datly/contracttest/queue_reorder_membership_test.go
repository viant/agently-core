package tests

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	convstore "github.com/viant/agently-core/app/store/conversation"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	auth "github.com/viant/agently-core/internal/auth"
	read "github.com/viant/agently-core/internal/datly/turnqueue/read"
	convservice "github.com/viant/agently-core/internal/service/conversation"
	reorder "github.com/viant/agently-core/internal/store/queuereorder"
	queue "github.com/viant/agently-core/internal/store/turnqueue"
	queuemodel "github.com/viant/agently-core/model/turnqueue"
)

func TestQueueReorderCurrentNativeMembershipExcludesStaleAndForeignRows(t *testing.T) {
	ctx := auth.WithUserInfo(context.Background(), &auth.UserInfo{Subject: "owner"})
	server, err := native.New(ctx, native.Options{WorkspaceRoot: t.TempDir()})
	require.NoError(t, err)
	defer server.Shutdown(ctx)
	conv, err := convservice.New(ctx, server)
	require.NoError(t, err)
	ds := data.NewService(server)
	patcher := ds.(interface {
		PatchTurnQueue(context.Context, *queuemodel.TurnQueue) error
	})
	for _, id := range []string{"owned", "foreign"} {
		row := convstore.NewConversation()
		row.SetId(id)
		row.SetVisibility("private")
		row.SetCreatedByUserID("owner")
		row.SetStatus("active")
		require.NoError(t, conv.PatchConversations(ctx, row))
	}
	for _, item := range []struct {
		id, conversation, status string
		seq                      int64
	}{
		{"live-first", "owned", "queued", 1}, {"stale-completed", "owned", "succeeded", 2}, {"stale-canceled", "owned", "canceled", 3}, {"live-last", "owned", "queued", 4}, {"foreign-turn", "foreign", "queued", 0},
	} {
		turn := convstore.NewTurn()
		turn.SetId(item.id)
		turn.SetConversationID(item.conversation)
		turn.SetStatus(item.status)
		turn.SetQueueSeq(item.seq)
		turn.SetStartedByMessageID("message-" + item.id)
		turn.SetCreatedAt(time.Now().UTC())
		require.NoError(t, conv.PatchTurn(ctx, turn))
		msg := convstore.NewMessage()
		msg.SetId("message-" + item.id)
		msg.SetConversationID(item.conversation)
		msg.SetTurnID(item.id)
		msg.SetRole("user")
		msg.SetType("task")
		msg.SetContent("full queued content")
		require.NoError(t, conv.PatchMessage(ctx, msg))
		if item.id == "foreign-turn" {
			continue // The single queue row for this turn is the mismatch below.
		}
		row := &queuemodel.TurnQueue{Has: &queuemodel.TurnQueueHas{}}
		row.SetId("queue-" + item.id)
		row.SetConversationId(item.conversation)
		row.SetTurnId(item.id)
		row.SetMessageId("message-" + item.id)
		row.SetQueueSeq(item.seq)
		row.SetStatus("queued")
		require.NoError(t, patcher.PatchTurnQueue(ctx, row))
	}
	// A queue reference with the requested conversation but a foreign native turn
	// is also excluded; a foreign-key-only match would incorrectly accept it.
	foreignStarter := convstore.NewMessage()
	foreignStarter.SetId("foreign-mismatch-message")
	foreignStarter.SetConversationID("foreign")
	foreignStarter.SetTurnID("foreign-turn")
	foreignStarter.SetRole("user")
	foreignStarter.SetType("task")
	foreignStarter.SetContent("foreign mismatch starter")
	require.NoError(t, conv.PatchMessage(ctx, foreignStarter))
	mismatched := &queuemodel.TurnQueue{Has: &queuemodel.TurnQueueHas{}}
	mismatched.SetId("mismatched-conversation")
	mismatched.SetConversationId("owned")
	mismatched.SetTurnId("foreign-turn")
	mismatched.SetMessageId("foreign-mismatch-message")
	mismatched.SetQueueSeq(2)
	mismatched.SetStatus("queued")
	require.NoError(t, patcher.PatchTurnQueue(ctx, mismatched))
	roster := func(onlyNative bool) []*read.QueueRowView {
		input := &read.QueueRowsInput{}
		input.SetConversationId("owned")
		input.SetQueueStatus("queued")
		input.SetNativeQueuedOnly(onlyNative)
		rows, err := (&queue.Store{Invoker: server}).List(ctx, input)
		require.NoError(t, err)
		return rows
	}
	require.Len(t, roster(false), 5, "default reader retains its historical row semantics")
	rows := roster(true)
	require.Len(t, rows, 2)
	require.Equal(t, "live-first", rows[0].TurnId)
	require.Equal(t, "live-last", rows[1].TurnId)
	require.ErrorIs(t, reorder.Move(ctx, server, "owned", "stale-completed", "up"), reorder.ErrTurnNotQueued)
	require.ErrorIs(t, reorder.Move(ctx, server, "owned", "stale-canceled", "down"), reorder.ErrTurnNotQueued)
	require.ErrorIs(t, reorder.Move(ctx, server, "owned", "foreign-turn", "up"), reorder.ErrTurnNotQueued)
	require.NoError(t, reorder.Move(ctx, server, "owned", "live-last", "up"))
	rows = roster(true)
	require.Equal(t, "live-last", rows[0].TurnId)
	require.EqualValues(t, 1, rows[0].QueueSeq)
	require.Equal(t, "live-first", rows[1].TurnId)
	require.EqualValues(t, 4, rows[1].QueueSeq)
	// Native turn and queue sequence swap together; stale rows remain untouched.
	canonical, err := conv.GetConversation(ctx, "owned", convstore.WithIncludeTranscript(true))
	require.NoError(t, err)
	found := map[string]int64{}
	for _, turn := range canonical.GetTranscript() {
		if turn.QueueSeq != nil {
			found[turn.Id] = int64(*turn.QueueSeq)
		}
	}
	require.EqualValues(t, 1, found["live-last"])
	require.EqualValues(t, 4, found["live-first"])
	require.EqualValues(t, 2, found["stale-completed"])
	require.EqualValues(t, 3, found["stale-canceled"])
	require.NoError(t, reorder.Move(ctx, server, "owned", "live-last", "down"))
	rows = roster(true)
	require.Equal(t, "live-first", rows[0].TurnId)
	require.Equal(t, "live-last", rows[1].TurnId)
}
