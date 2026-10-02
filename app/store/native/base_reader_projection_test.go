package native_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	conversation "github.com/viant/agently-core/internal/store/conversation"
	"github.com/viant/xdatly/state"
)

func TestWorkspaceRuntimeBaseReadersDoNotFetchGraphSQLite(t *testing.T) {
	native, _, db := orphanFixture(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := db.Exec("INSERT INTO conversation(id,created_at,status,title) VALUES('base-owned',?,'succeeded','scalar title')", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,created_at,role,type,content) VALUES('base-message','base-owned',?,'assistant','text','scalar content')", now)
	require.NoError(t, err)
	_, err = db.Exec("DROP TABLE model_call")
	require.NoError(t, err)
	_, err = db.Exec("DROP TABLE tool_call")
	require.NoError(t, err)
	ctx := context.Background()
	conversations := &conversation.Store{Invoker: native.Invoker, OwnerID: authctx.EffectiveUserID}
	base, err := conversations.GetBaseInternal(ctx, "base-owned")
	require.NoError(t, err)
	require.Equal(t, "scalar title", *base.Title)
	require.Nil(t, base.Transcript)
	require.Nil(t, base.Usage)
	require.Equal(t, "waiting", base.Stage, "canonical fallback hook preserves stage when no turn graph is present")
	messages := &conversation.MessageStore{Invoker: native.Invoker, OwnerID: authctx.EffectiveUserID}
	input := &msgread.MessagesInput{}
	input.SetConversationId("base-owned")
	rows, err := messages.ListRows(ctx, input, state.Selectors{&state.NamedSelector{Name: "reader", Selector: state.Selector{Limit: 1, OrderBy: "created_at DESC,id DESC"}}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "scalar content", *rows[0].Content)
	require.Nil(t, rows[0].ModelCall)
	require.Nil(t, rows[0].MessageToolCall)
	require.Nil(t, rows[0].Elicitation)
}
