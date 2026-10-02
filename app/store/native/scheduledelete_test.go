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
	authctx "github.com/viant/agently-core/internal/auth"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/agently-core/internal/store/scheduledelete"
)

func scheduleDeleteFixture(t *testing.T) (*scheduledelete.Store, *sql.DB, context.Context) {
	t.Helper()
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	_, file, _, _ := runtime.Caller(0)
	root := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(root, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	return &scheduledelete.Store{Invoker: server, OwnerID: authctx.EffectiveUserID}, db, ctx
}
func seedScheduleDelete(t *testing.T, db *sql.DB, graph bool) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	_, err := db.Exec(`INSERT INTO schedule(id,name,created_by_user_id,visibility,agent_ref,enabled,schedule_type,timezone,created_at) VALUES('s1','Schedule','u1','private','simple',1,'adhoc','UTC',?)`, now)
	require.NoError(t, err)
	if graph {
		_, err = db.Exec(`INSERT INTO conversation(id,title,created_by_user_id,visibility,status,schedule_id,created_at) VALUES('c1','root','u1','private','succeeded','s1',?)`, now)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO conversation(id,title,created_by_user_id,visibility,status,conversation_parent_id,created_at) VALUES('c2','child','u1','private','succeeded','c1',?)`, now)
		require.NoError(t, err)
	}
	var conversation any
	if graph {
		conversation = "c1"
	}
	_, err = db.Exec(`INSERT INTO run(id,schedule_id,conversation_kind,status,effective_user_id,conversation_id,created_at) VALUES('r1','s1','scheduled','succeeded','u1',?,?)`, conversation, now)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO run(id,schedule_id,conversation_kind,status,effective_user_id,created_at) VALUES('r2','s1','scheduled','succeeded','u1',?)`, now)
	require.NoError(t, err)
}
func scheduleDeleteCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	require.Equal(t, want, count, table)
}
func TestWorkspaceRuntimeScheduleCascadeDelete(t *testing.T) {
	t.Run("schedule with tree and unattached run", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, true)
		require.NoError(t, store.DeleteSchedule(ctx, " s1 "))
		scheduleDeleteCount(t, db, "schedule", 0)
		scheduleDeleteCount(t, db, "conversation", 0)
		scheduleDeleteCount(t, db, "run", 0)
		require.ErrorIs(t, store.DeleteSchedule(ctx, "s1"), scheduledelete.ErrScheduleNotFound)
	})
	t.Run("one run preserves schedule and other run", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, true)
		require.NoError(t, store.DeleteRun(ctx, "r1"))
		scheduleDeleteCount(t, db, "schedule", 1)
		scheduleDeleteCount(t, db, "conversation", 0)
		scheduleDeleteCount(t, db, "run", 1)
		require.NoError(t, store.DeleteRun(ctx, "r2"))
		scheduleDeleteCount(t, db, "schedule", 1)
		scheduleDeleteCount(t, db, "run", 0)
	})
	t.Run("owner denied", func(t *testing.T) {
		store, db, _ := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, true)
		other := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})
		require.ErrorIs(t, store.DeleteSchedule(other, "s1"), tree.ErrPermissionDenied)
		require.ErrorIs(t, store.DeleteRun(other, "r1"), tree.ErrPermissionDenied)
		scheduleDeleteCount(t, db, "schedule", 1)
		scheduleDeleteCount(t, db, "conversation", 2)
		scheduleDeleteCount(t, db, "run", 2)
	})
	t.Run("internal schedule run hidden", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, false)
		_, err := db.Exec("UPDATE schedule SET internal=1 WHERE id='s1'")
		require.NoError(t, err)
		require.ErrorIs(t, store.DeleteRun(ctx, "r1"), scheduledelete.ErrScheduledRunNotFound)
		scheduleDeleteCount(t, db, "run", 2)
	})
	t.Run("live run blocks and stale run allows", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, false)
		now := time.Now().UTC()
		_, err := db.Exec("UPDATE run SET status='running',lease_until=?,last_heartbeat_at=?,heartbeat_interval_sec=5 WHERE id='r1'", now.Add(time.Minute).Format(time.RFC3339), now.Format(time.RFC3339))
		require.NoError(t, err)
		require.ErrorIs(t, store.DeleteSchedule(ctx, "s1"), tree.ErrConversationActive)
		scheduleDeleteCount(t, db, "run", 2)
		_, err = db.Exec("UPDATE run SET lease_until=?,last_heartbeat_at=?,started_at=? WHERE id='r1'", now.Add(-time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339), now.Add(-time.Hour).Format(time.RFC3339))
		require.NoError(t, err)
		require.NoError(t, store.DeleteRun(ctx, "r1"))
		scheduleDeleteCount(t, db, "run", 1)
	})
	t.Run("legacy run deletes independently of unscheduled identity collision", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, false)
		_, err := db.Exec(`CREATE TABLE schedule_run (
   id TEXT PRIMARY KEY, schedule_id TEXT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
   updated_at DATETIME, status TEXT NOT NULL DEFAULT 'pending', error_message TEXT,
   lease_owner TEXT, lease_until DATETIME, precondition_ran_at DATETIME, precondition_passed INTEGER,
   precondition_result TEXT, conversation_id TEXT, conversation_kind TEXT DEFAULT 'scheduled',
   scheduled_for DATETIME, started_at DATETIME, completed_at DATETIME);
   INSERT INTO schedule_run(id,schedule_id,status) VALUES('legacy-only','s1','succeeded');
   INSERT INTO run(id,conversation_kind,status,created_at) VALUES('legacy-only','interactive','succeeded',CURRENT_TIMESTAMP)`)
		require.NoError(t, err)
		require.NoError(t, store.DeleteRun(ctx, "legacy-only"))
		scheduleDeleteCount(t, db, "schedule_run", 0)
		scheduleDeleteCount(t, db, "run", 3)
		scheduleDeleteCount(t, db, "schedule", 1)
	})

	t.Run("corrupt schedule lease conservatively blocks", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, false)
		_, err := db.Exec("UPDATE schedule SET lease_until='corrupt' WHERE id='s1'")
		require.NoError(t, err)
		require.ErrorIs(t, store.DeleteSchedule(ctx, "s1"), tree.ErrConversationActive)
		scheduleDeleteCount(t, db, "schedule", 1)
		scheduleDeleteCount(t, db, "run", 2)
	})

	t.Run("unowned schedule without graph preserves legacy unscoped policy", func(t *testing.T) {
		store, db, _ := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, false)
		_, err := db.Exec("UPDATE schedule SET created_by_user_id=NULL WHERE id='s1'")
		require.NoError(t, err)
		require.NoError(t, store.DeleteSchedule(context.Background(), "s1"))
		scheduleDeleteCount(t, db, "schedule", 0)
		scheduleDeleteCount(t, db, "run", 0)
	})

	t.Run("late schedule failure rolls back tree and runs", func(t *testing.T) {
		store, db, ctx := scheduleDeleteFixture(t)
		seedScheduleDelete(t, db, true)
		_, err := db.Exec("CREATE TRIGGER reject_schedule_delete BEFORE DELETE ON schedule BEGIN SELECT RAISE(ABORT,'late schedule failure'); END")
		require.NoError(t, err)
		require.ErrorContains(t, store.DeleteSchedule(ctx, "s1"), "late schedule failure")
		scheduleDeleteCount(t, db, "schedule", 1)
		scheduleDeleteCount(t, db, "conversation", 2)
		scheduleDeleteCount(t, db, "run", 2)
	})
}
