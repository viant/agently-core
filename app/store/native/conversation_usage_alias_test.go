package native_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	convstore "github.com/viant/agently-core/internal/store/conversation"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/xdatly/state"
)

func TestWorkspaceRuntimeConversationUsageAliasSQLite(t *testing.T) {
	store, _, db := orphanFixture(t)
	runConversationUsageAlias(t, store.Invoker, db, "usage-alias-sqlite-")
}

func TestWorkspaceRuntimeConversationUsageAliasMySQL(t *testing.T) {
	store, _, db, prefix := orphanMySQLFixture(t)
	runConversationUsageAlias(t, store.Invoker, db, prefix+"usage-")
}

func runConversationUsageAlias(t *testing.T, invoker dexec.ComponentInvoker, db *sql.DB, prefix string) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := db.Exec("INSERT INTO conversation(id,created_at,status) VALUES(?,?,'succeeded')", prefix+"conversation", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO message(id,conversation_id,role,type,created_at) VALUES(?,?,'assistant','text',?)", prefix+"message", prefix+"conversation", now)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO model_call(message_id,provider,model,model_kind,status,prompt_tokens,completion_tokens,total_tokens,cost) VALUES(?,'fixture','usage-model','chat','succeeded',11,7,18,0.25)", prefix+"message")
	require.NoError(t, err)
	store := &convstore.Store{Invoker: invoker, OwnerID: authctx.EffectiveUserID}
	row, err := store.GetInternal(ctx, prefix+"conversation", nil)
	require.NoError(t, err)
	require.NotNil(t, row.Usage, "SQL alias must retain generated Usage relation")
	require.Equal(t, 18, *row.Usage.TotalTokens)
	require.Equal(t, 0.25, *row.Usage.Cost)
	require.Len(t, row.Usage.Model, 1, "nested model relation must still join Usage")
	require.Equal(t, "usage-model", row.Usage.Model[0].Model)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	var wire map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &wire))
	require.Contains(t, wire, "usage", "public relation JSON name must stay unchanged")
	require.NotContains(t, wire, "data_usage")
	selected, err := store.GetInternal(ctx, prefix+"conversation", nil, state.Selectors{
		&state.NamedSelector{Name: "usage", Selector: state.Selector{Fields: []string{"conversation_id", "total_tokens"}}},
	})
	require.NoError(t, err)
	require.NotNil(t, selected.Usage)
	require.Equal(t, 18, *selected.Usage.TotalTokens)
	require.Nil(t, selected.Usage.Cost, "SQL alias changes must preserve the public usage selector name")
}
