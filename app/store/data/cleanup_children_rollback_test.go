package data

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Exercise a failure IN the second child batch, not just after all child
// deletes. Earlier call/file deletes and the first message batch must roll back.
func TestDeleteConversationTree_ChildBatchesRollbackSQLite(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForConversationTreeDelete)
	tx, err := db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	for i := 0; i < 401; i++ {
		_, err = tx.Exec(`INSERT INTO message(id,conversation_id,turn_id,role,type)
            VALUES(?,'conv-root','turn-root','assistant','text')`, fmt.Sprintf("zz-bulk-message-%03d", i))
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	counts := func() map[string]int {
		result := map[string]int{}
		for _, table := range []string{"conversation", "turn", "message", "model_call", "tool_call", "generated_file", "call_payload", "tool_approval_queue"} {
			var n int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
			result[table] = n
		}
		return result
	}
	before := counts()
	_, err = db.Exec(`CREATE TRIGGER fail_second_child_batch BEFORE DELETE ON message
        WHEN OLD.id = 'zz-bulk-message-400' BEGIN
        SELECT CASE WHEN (SELECT COUNT(*) FROM message WHERE id='zz-bulk-message-000') = 0
            THEN RAISE(ABORT,'forced failure after first child batch')
            ELSE RAISE(ABORT,'failure before first child batch') END;
        END`)
	require.NoError(t, err)
	require.ErrorContains(t, svc.DeleteConversationTree(deleteTestContext(), "conv-root"), "forced failure after first child batch")
	require.Equal(t, before, counts(), "all earlier mutations must roll back with the second message batch")
	_, err = db.Exec("DROP TRIGGER fail_second_child_batch")
	require.NoError(t, err)
	require.NoError(t, svc.DeleteConversationTree(deleteTestContext(), "conv-root"))
	require.Equal(t, map[string]int{"conversation": 1, "turn": 1, "message": 1,
		"model_call": 0, "tool_call": 0, "generated_file": 0, "call_payload": 1, "tool_approval_queue": 0}, counts())
}
