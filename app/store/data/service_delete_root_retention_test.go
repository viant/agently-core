package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestMaintainConversationTree_RootActivityCoalesce(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	recent := old.Add(48 * time.Hour)
	for _, tc := range []struct {
		name          string
		last, updated any
		want          time.Time
		eligible      bool
	}{
		{"last-before-updated", old, recent, old, true},
		{"updated-fallback", nil, recent, recent, false},
		{"created-fallback", nil, nil, old, true},
		{"recent-last", recent, old, recent, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newScheduledMaintenanceServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{{SQL: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"root-coalesce", old, tc.updated, tc.last, "running"}}})
			})
			req := maintenanceRequest("root-coalesce", "", old.Add(24*time.Hour))
			for _, mode := range []ConversationMaintenanceMode{ConversationMaintenanceDryRun, ConversationMaintenanceDelete} {
				req.Mode = mode
				result := maintainConversationForTest(t, svc, req)
				if result.Eligible != tc.eligible || result.Deleted != (tc.eligible && mode == ConversationMaintenanceDelete) || !result.LastActivity.Equal(tc.want) {
					t.Fatalf("mode=%s result=%#v", mode, result)
				}
			}
			remaining := 1
			if tc.eligible {
				remaining = 0
			}
			assertStage1RowCount(t, db, "conversation", "id", "root-coalesce", remaining)
		})
	}
}

func TestMaintainScheduledRun_QualifiesOldRootNotRecentExecutionOrChild(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-60 * 24 * time.Hour)
	cutoff := now.Add(-30 * 24 * time.Hour)
	svc, db := newScheduledMaintenanceServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, agent_ref) VALUES ('root-retention-schedule', 'test', 'agent')`},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, schedule_id, schedule_run_id) VALUES ('scheduled-age-root', ?, ?, 'root-retention-schedule', 'recent-execution')`, Params: []interface{}{old, old}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES ('scheduled-parent-turn', 'scheduled-age-root', 'running')`},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, conversation_parent_turn_id, schedule_run_id) VALUES ('scheduled-age-child', ?, ?, 'scheduled-parent-turn', 'recent-execution')`, Params: []interface{}{now, now}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, updated_at, lease_until, last_heartbeat_at, heartbeat_interval_sec) VALUES ('recent-execution', 'root-retention-schedule', 'scheduled-age-root', 'scheduled', 'running', ?, ?, ?, ?, 5)`, Params: []interface{}{old, now, now.Add(time.Hour), now}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, schedule_id, schedule_run_id) VALUES ('sibling-execution-root', ?, ?, 'root-retention-schedule', 'sibling-execution')`, Params: []interface{}{now, now}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, updated_at) VALUES ('sibling-execution', 'root-retention-schedule', 'sibling-execution-root', 'scheduled', 'running', ?, ?)`, Params: []interface{}{now, now}},
		})
	})
	candidates, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), ScheduledRunMaintenanceCandidateRequest{InactiveBefore: cutoff, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertScheduledMaintenanceCandidateIDs(t, candidates, "recent-execution")
	if !candidates[0].ActivityAt.Equal(old) {
		t.Fatalf("candidate=%#v", candidates[0])
	}
	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{RunID: "recent-execution", InactiveBefore: cutoff, Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Deleted || !result.LastActivity.Equal(old) || result.ConversationCount != 2 {
		t.Fatalf("result=%#v", result)
	}
	for _, id := range []string{"scheduled-age-root", "scheduled-age-child"} {
		assertStage1RowCount(t, db, "conversation", "id", id, 0)
	}
	assertStage1RowCount(t, db, "run", "id", "recent-execution", 0)
	assertStage1RowCount(t, db, "run", "id", "sibling-execution", 1)
	assertStage1RowCount(t, db, "conversation", "id", "sibling-execution-root", 1)
	assertStage1RowCount(t, db, "schedule", "id", "root-retention-schedule", 1)
}

func TestMaintainConversationTree_QualifiedRootDeletesLiveProtocolRun(t *testing.T) {
	svc, db, store := aguiDeleteFixture(t)
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	aguiDeleteSeedRun(t, store, "agui-root", "u1", 0, false)
	if _, err := db.Exec(`UPDATE conversation SET created_at=?, last_activity=? WHERE id IN ('agui-root','agui-child')`, old, old); err != nil {
		t.Fatal(err)
	}
	result := maintainConversationForTest(t, svc, ConversationMaintenanceRequest{RootID: "agui-root", Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(time.Hour), Mode: ConversationMaintenanceDelete})
	if !result.Deleted {
		t.Fatalf("result=%#v", result)
	}
	assertStage1RowCount(t, db, "run", "conversation_id", "agui-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "agui-root", 0)
	assertStage1RowCount(t, db, "run", "id", "shared-external", 1)
}

func TestMaintainConversationTree_QualifiedRootDeletesActiveExport(t *testing.T) {
	for _, status := range []string{"queued", "running"} {
		t.Run(status, func(t *testing.T) {
			old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
			now := time.Now().UTC().Format(time.RFC3339)
			svc, db := newScheduledMaintenanceServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES ('export-age-root', ?, ?, 'succeeded')`, Params: []interface{}{old, old}},
					{SQL: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id, updated_at) VALUES ('export-age-report', 'u1', 'export-age-root', 'test', 'completed', ?, ?, 1, 'export-age-request', ?)`, Params: []interface{}{now, now, now}},
					{SQL: `INSERT INTO report_export_job (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at, report_run_id, report_run_revision, export_request_id) VALUES ('export-age-job', 'external://report', 'u1', 'export-age-root', 'pdf', 'draft', ?, ?, 'export-age-report', 1, 'export-age-export')`, Params: []interface{}{status, now}},
				})
			})
			result := maintainConversationForTest(t, svc, ConversationMaintenanceRequest{RootID: "export-age-root", Kind: ConversationMaintenanceInteractive, InactiveBefore: old.Add(time.Hour), Mode: ConversationMaintenanceDelete})
			if !result.Deleted {
				t.Fatalf("result=%#v", result)
			}
			assertStage1RowCount(t, db, "report_export_job", "job_id", "export-age-job", 0)
		})
	}
}

func TestMaintainScheduledRun_ParentCycleIsNotRunOnly(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newScheduledMaintenanceServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id,name,agent_ref) VALUES ('cycle-schedule','test','agent')`},
			{SQL: `INSERT INTO conversation (id,created_at,last_activity,schedule_run_id) VALUES ('cycle-a',?,?,'cycle-run')`, Params: []interface{}{old, old}},
			{SQL: `INSERT INTO conversation (id,created_at,last_activity,conversation_parent_id,schedule_run_id) VALUES ('cycle-b',?,?,'cycle-a','cycle-run')`, Params: []interface{}{old, old}},
			{SQL: `UPDATE conversation SET conversation_parent_id='cycle-b' WHERE id='cycle-a'`},
			{SQL: `INSERT INTO run (id,schedule_id,conversation_id,status,created_at) VALUES ('cycle-run','cycle-schedule','cycle-a','succeeded',?)`, Params: []interface{}{old}},
		})
	})
	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{RunID: "cycle-run", InactiveBefore: old.Add(time.Hour), Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceGraphReferenced {
		t.Fatalf("result=%#v", result)
	}
	assertStage1RowCount(t, db, "run", "id", "cycle-run", 1)
	assertStage1RowCount(t, db, "conversation", "id", "cycle-a", 1)
	assertStage1RowCount(t, db, "conversation", "id", "cycle-b", 1)
}

func TestMaintainScheduledRun_RecentRootIsNotQualifiedByOldExecution(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-60 * 24 * time.Hour)
	cutoff := now.Add(-30 * 24 * time.Hour)
	svc, db := newScheduledMaintenanceServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id,name,agent_ref) VALUES ('recent-root-schedule','test','agent')`},
			{SQL: `INSERT INTO conversation (id,created_at,last_activity,schedule_run_id) VALUES ('recent-root',?,?,'old-execution')`, Params: []interface{}{old, now}},
			{SQL: `INSERT INTO run (id,schedule_id,conversation_id,status,created_at,completed_at) VALUES ('old-execution','recent-root-schedule','recent-root','succeeded',?,?)`, Params: []interface{}{old, old}},
		})
	})
	candidates, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), ScheduledRunMaintenanceCandidateRequest{InactiveBefore: cutoff, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("recent root entered candidate scan: %#v", candidates)
	}
	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{RunID: "old-execution", InactiveBefore: cutoff, Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceRecentActivity || !result.LastActivity.Equal(now) {
		t.Fatalf("result=%#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "recent-root", 1)
	assertStage1RowCount(t, db, "run", "id", "old-execution", 1)
}

// Exercise both generated candidate queries and the actual deletion on the
// dedicated disposable MySQL benchmark schema, never an ordinary database.
func TestMaintainScheduledRun_RootRetentionMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_CLEANUP_BENCHMARK_DSN is not set")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	require.Equal(t, "tcp", cfg.Net)
	require.Equal(t, "127.0.0.1:3308", cfg.Addr)
	require.Equal(t, "cleanup_bench_verify_local", cfg.DBName)
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	svc := NewService(newNativeMySQLRuntime(t, dsn))
	now := time.Now().UTC().Truncate(time.Second)
	old, cutoff := now.Add(-60*24*time.Hour), now.Add(-30*24*time.Hour)
	for _, source := range []string{"run", "schedule_run"} {
		for _, recentRoot := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/recent-root=%t", source, recentRoot), func(t *testing.T) {
				tag := fmt.Sprintf("root-age-%d", time.Now().UnixNano())
				scheduleID, rootID, runID := tag+"-schedule", tag+"-root", tag+"-run"
				leaseKey := tag + "-lease"
				t.Cleanup(func() {
					for _, statement := range []struct{ query, id string }{
						{"DELETE FROM " + source + " WHERE id=?", runID},
						{"DELETE FROM conversation WHERE id=?", rootID},
						{"DELETE FROM schedule WHERE id=?", scheduleID},
						{"DELETE FROM maintenance_lease WHERE lease_key=?", leaseKey},
					} {
						_, err := db.Exec(statement.query, statement.id)
						require.NoError(t, err)
					}
				})
				_, err := db.Exec(`INSERT INTO schedule(id,name,agent_ref) VALUES(?,?,'agent')`, scheduleID, tag)
				require.NoError(t, err)
				rootActivity, runActivity := old, now
				if recentRoot {
					rootActivity, runActivity = now, old
				}
				_, err = db.Exec(`INSERT INTO conversation(id,created_at,last_activity,schedule_run_id) VALUES(?,?,?,?)`, rootID, old, rootActivity, runID)
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO `+source+`(id,schedule_id,conversation_id,conversation_kind,status,created_at,updated_at) VALUES(?,?,?,'scheduled','succeeded',?,?)`, runID, scheduleID, rootID, old, runActivity)
				require.NoError(t, err)
				candidates, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), ScheduledRunMaintenanceCandidateRequest{InactiveBefore: cutoff, Limit: 100})
				require.NoError(t, err)
				found := false
				for _, candidate := range candidates {
					if candidate.RunID == runID {
						found = true
						require.True(t, candidate.ActivityAt.Equal(rootActivity))
					}
				}
				require.Equal(t, !recentRoot, found)
				result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{RunID: runID, InactiveBefore: cutoff, Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, leaseKey, tag)})
				require.NoError(t, err)
				require.Equal(t, !recentRoot, result.Deleted)
				require.Equal(t, !recentRoot, result.Eligible)
				require.True(t, result.LastActivity.Equal(rootActivity))
				want := 0
				if recentRoot {
					want = 1
				}
				assertStage1RowCount(t, db, source, "id", runID, want)
				assertStage1RowCount(t, db, "conversation", "id", rootID, want)
				assertStage1RowCount(t, db, "schedule", "id", scheduleID, 1)
			})
		}
	}
}
