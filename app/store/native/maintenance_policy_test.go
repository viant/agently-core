package native_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	conversation "github.com/viant/agently-core/internal/store/conversationmaintenance"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	scheduled "github.com/viant/agently-core/internal/store/scheduledmaintenance"
	"github.com/viant/datly/standalone"
)

func maintenanceRuntime(t *testing.T) (*standalone.Server, *sql.DB) {
	t.Helper()
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	_, file, _, _ := runtime.Caller(0)
	workspace := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return server, db
}
func nativeMaintenanceLease(t *testing.T, server *standalone.Server) maintenance.Lease {
	t.Helper()
	result, err := (&maintenance.Store{Invoker: server}).Acquire(context.Background(), "maintenance-native-policy", "worker", time.Minute)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	return result.Lease
}

func TestWorkspaceRuntimeScheduledMaintenanceCandidatesAndPolicies(t *testing.T) {
	server, db := maintenanceRuntime(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := old.Add(time.Hour)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref,created_by_user_id) VALUES('s','s','test',' schedule-owner ');
 INSERT INTO run(id,schedule_id,status,created_at,effective_user_id) VALUES('A','s','succeeded','2026-01-01 00:00:00','other'),('a','s','succeeded','2026-01-01 00:00:00','other'),('shadow',NULL,'succeeded','2026-01-01 00:00:00','other'),('missing','missing-schedule','succeeded','2026-01-01 00:00:00','other');`)
	require.NoError(t, err)
	store := &scheduled.Store{Invoker: server}
	rows, err := store.Candidates(ctx, scheduled.CandidateRequest{InactiveBefore: cutoff, Limit: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "A", rows[0].RunID)
	require.Equal(t, "schedule-owner", rows[0].ExpectedOwnerID)
	next, err := store.Candidates(ctx, scheduled.CandidateRequest{InactiveBefore: cutoff, AfterActivity: rows[0].ActivityAt, AfterRunID: rows[0].RunID, Limit: 10})
	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, "a", next[0].RunID)
	dry, err := store.Maintain(ctx, &scheduled.Input{RunID: "A", InactiveBefore: cutoff})
	require.NoError(t, err)
	require.True(t, dry.Eligible)
	require.Equal(t, "eligible", dry.Reason)
	missing, err := store.Maintain(ctx, &scheduled.Input{RunID: "missing", InactiveBefore: cutoff})
	require.NoError(t, err)
	require.Equal(t, "schedule_missing", missing.Reason)
	fenced := nativeMaintenanceLease(t, server)
	deleted, err := store.Maintain(ctx, &scheduled.Input{RunID: "A", InactiveBefore: cutoff, Mode: scheduled.DeleteMode, Lease: fenced})
	require.NoError(t, err)
	require.True(t, deleted.Deleted)
	require.Equal(t, "deleted", deleted.Reason)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schedule WHERE id='s'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM run WHERE id='a'`).Scan(&count))
	require.Equal(t, 1, count, "unrelated run remains")
}

func TestWorkspaceRuntimeConversationMaintenanceSystemFallbackAndRollback(t *testing.T) {
	server, db := maintenanceRuntime(t)
	ctx := context.Background()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cutoff := old.Add(time.Hour)
	_, err := db.Exec(`INSERT INTO conversation(id,created_by_user_id,status,created_at,last_activity) VALUES('root',NULL,'succeeded','2026-01-01 00:00:00','2026-01-01 00:00:00'),('child','different-owner','failed','2026-01-01 00:00:00','2026-01-01 00:00:00'),('fallback',NULL,'succeeded','2026-01-01 00:00:00','2026-01-01 00:00:00'),('dangling',NULL,'succeeded','2026-01-01 00:00:00','2026-01-01 00:00:00'),('recent',NULL,'succeeded','2026-01-01 00:00:00','2026-01-03 00:00:00');
 UPDATE conversation SET conversation_parent_id='root' WHERE id='child';
 UPDATE conversation SET schedule_run_id='missing-legacy-marker' WHERE id IN('fallback','dangling');
 INSERT INTO turn(id,conversation_id,status,run_id,created_at) VALUES('dangling-turn','dangling','succeeded','missing-current-run','2026-01-01 00:00:00');`)
	require.NoError(t, err)
	store := &conversation.Store{Invoker: server}
	fallback, err := store.Maintain(ctx, &conversation.Input{RootID: "fallback", Kind: conversation.Fallback, InactiveBefore: cutoff, Mode: scheduled.DryRun})
	require.NoError(t, err)
	require.True(t, fallback.Eligible)
	dangling, err := store.Maintain(ctx, &conversation.Input{RootID: "dangling", Kind: conversation.Fallback, InactiveBefore: cutoff, Mode: scheduled.DryRun})
	require.NoError(t, err)
	require.Equal(t, "run_present", dangling.Reason, "raw dangling current reference owns fallback shell")
	candidates, err := store.Candidates(ctx, conversation.CandidateRequest{Kind: conversation.Fallback, InactiveBefore: cutoff, Limit: 10})
	require.NoError(t, err)
	require.Len(t, candidates, 2, "candidate scan excludes actual root conversation-run rows, while evaluator also checks raw turn links")
	fenced := nativeMaintenanceLease(t, server)
	var before string
	require.NoError(t, db.QueryRow(`SELECT CAST(updated_at AS CHAR) FROM maintenance_lease WHERE lease_key=?`, fenced.Key).Scan(&before))
	skipped, err := store.Maintain(ctx, &conversation.Input{RootID: "recent", Kind: conversation.Interactive, InactiveBefore: cutoff, Mode: scheduled.DeleteMode, Lease: fenced})
	require.NoError(t, err)
	require.Equal(t, "recent_activity", skipped.Reason)
	var after string
	require.NoError(t, db.QueryRow(`SELECT CAST(updated_at AS CHAR) FROM maintenance_lease WHERE lease_key=?`, fenced.Key).Scan(&after))
	require.Equal(t, before, after, "skipped deletion rolls back fence update")
	_, err = db.Exec(`CREATE TRIGGER maintenance_late_fail BEFORE DELETE ON conversation WHEN OLD.id='root' BEGIN SELECT RAISE(ABORT,'maintenance rollback'); END;`)
	require.NoError(t, err)
	_, err = store.Maintain(ctx, &conversation.Input{RootID: "root", Kind: conversation.Interactive, InactiveBefore: cutoff, Mode: scheduled.DeleteMode, Lease: fenced})
	require.Error(t, err)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM conversation WHERE id IN('root','child')`).Scan(&count))
	require.Equal(t, 2, count, "late writer failure rolls back whole graph")
	_, err = db.Exec(`DROP TRIGGER maintenance_late_fail`)
	require.NoError(t, err)
	deleted, err := store.Maintain(ctx, &conversation.Input{RootID: "root", Kind: conversation.Interactive, InactiveBefore: cutoff, Mode: scheduled.DeleteMode, Lease: fenced})
	require.NoError(t, err)
	require.True(t, deleted.Deleted)
	require.Equal(t, 2, deleted.ConversationCount)
}

func TestWorkspaceRuntimeScheduledMaintenanceFractionalCursorMatchesSQLContract(t *testing.T) {
	server, db := maintenanceRuntime(t)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref) VALUES('fractional-s','fractional','test');
 INSERT INTO run(id,schedule_id,status,created_at) VALUES
 ('upper','fractional-s','succeeded','2026-01-01 00:00:00.123456'),
 ('z','fractional-s','succeeded','2026-01-01 00:00:00.123456'),
 ('next','fractional-s','succeeded','2026-01-01 00:00:00.123457'),
 ('cutoff','fractional-s','succeeded','2026-01-01 00:00:01'),
 ('newer','fractional-s','succeeded','2026-01-01 00:00:01.000001');`)
	require.NoError(t, err)
	cutoff := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	store := &scheduled.Store{Invoker: server}
	var actual []string
	request := scheduled.CandidateRequest{InactiveBefore: cutoff, Limit: 1}
	for {
		rows, err := store.Candidates(context.Background(), request)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		require.Len(t, rows, 1)
		actual = append(actual, rows[0].RunID)
		request.AfterActivity, request.AfterRunID = rows[0].ActivityAt, rows[0].RunID
		require.LessOrEqual(t, len(actual), 5, "cursor must advance")
	}
	rows, err := db.Query(`SELECT r.id FROM run r JOIN schedule s ON s.id=r.schedule_id WHERE COALESCE(r.completed_at,r.updated_at,r.created_at)<=? ORDER BY COALESCE(r.completed_at,r.updated_at,r.created_at),HEX(r.id)`, cutoff.UTC().Format("2006-01-02 15:04:05.999999999"))
	require.NoError(t, err)
	defer rows.Close()
	var expected []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		expected = append(expected, id)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"upper", "z", "next", "cutoff"}, expected)
	require.Equal(t, expected, actual)
}

func TestWorkspaceRuntimeMaintenanceMixedTimestampChronology(t *testing.T) {
	server, db := maintenanceRuntime(t)
	_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref) VALUES('mixed-s','mixed','test')`)
	require.NoError(t, err)
	fixtures := []struct{ id, raw string }{
		{"prior", "2025-12-31T23:59:59.999999999Z"},
		{"A", "2026-01-01 00:00:00"}, {"a", "2026-01-01T00:00:00Z"},
		{"nano1", "2026-01-01 00:00:00.000000001"},
		{"nano2", "2026-01-01T05:30:00.000000002+05:30"},
		{"rollover", "2025-12-31 17:00:00.000000003 -0700 MST"},
		{"cutoff", "2026-01-01 00:00:01+00:00"},
		{"after", "2026-01-01T00:00:01.000000001Z"},
		{"malformed", "not-a-time"},
	}
	for _, fixture := range fixtures {
		_, err = db.Exec(`INSERT INTO run(id,schedule_id,status,created_at) VALUES(?,'mixed-s','succeeded',?)`, fixture.id, fixture.raw)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO conversation(id,status,created_at,created_by_user_id) VALUES(?,'succeeded',?,'mixed-owner')`, fixture.id, fixture.raw)
		require.NoError(t, err)
	}
	cutoff := time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)
	wanted := []string{"prior", "A", "a", "nano1", "nano2", "rollover", "cutoff"}
	scheduledStore := &scheduled.Store{Invoker: server}
	scheduledRequest := scheduled.CandidateRequest{InactiveBefore: cutoff, Limit: 1}
	var scheduledIDs []string
	for {
		rows, err := scheduledStore.Candidates(context.Background(), scheduledRequest)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		require.Len(t, rows, 1)
		scheduledIDs = append(scheduledIDs, rows[0].RunID)
		require.LessOrEqual(t, len(scheduledIDs), len(wanted), "cursor must advance")
		scheduledRequest.AfterActivity, scheduledRequest.AfterRunID = rows[0].ActivityAt, rows[0].RunID
	}
	require.Equal(t, wanted, scheduledIDs, "SQL/RFC/offset spellings must share chronological keys")
	conversationStore := &conversation.Store{Invoker: server}
	conversationRequest := conversation.CandidateRequest{Kind: conversation.Interactive, InactiveBefore: cutoff, Limit: 1}
	var conversationIDs []string
	for {
		rows, err := conversationStore.Candidates(context.Background(), conversationRequest)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		require.Len(t, rows, 1)
		conversationIDs = append(conversationIDs, rows[0].RootID)
		require.LessOrEqual(t, len(conversationIDs), len(wanted), "cursor must advance")
		conversationRequest.AfterActivity, conversationRequest.AfterRootID = rows[0].ActivityAt, rows[0].RootID
	}
	require.Equal(t, wanted, conversationIDs)
	scheduledRequest = scheduled.CandidateRequest{InactiveBefore: time.Date(2026, 1, 1, 0, 0, 0, 2, time.UTC), Limit: 100}
	adjacent, err := scheduledStore.Candidates(context.Background(), scheduledRequest)
	require.NoError(t, err)
	var adjacentIDs []string
	for _, row := range adjacent {
		adjacentIDs = append(adjacentIDs, row.RunID)
	}
	require.Equal(t, wanted[:5], adjacentIDs, "cutoff at second adjacent nanosecond must include exactly that instant")
}
