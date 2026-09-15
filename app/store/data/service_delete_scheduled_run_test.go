package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestDeleteScheduledRun_DeletesGraphOrRunAndKeepsSchedule(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForScheduledRunDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteScheduledRun(ctx, "scheduled-run-graph"); err != nil {
		t.Fatalf("DeleteScheduledRun(graph) error: %v", err)
	}
	assertDeleteRowCount(t, db, "schedule", "scheduled-run-schedule", 1)
	assertDeleteRowCount(t, db, "run", "scheduled-run-graph", 0)
	assertDeleteRowCount(t, db, "conversation", "scheduled-run-conversation", 0)
	assertDeleteRowCount(t, db, "conversation", "scheduled-run-child", 0)
	assertDeleteRowCount(t, db, "run", "scheduled-run-no-graph", 1)

	if err := svc.DeleteScheduledRun(ctx, "scheduled-run-no-graph"); err != nil {
		t.Fatalf("DeleteScheduledRun(no graph) error: %v", err)
	}
	assertDeleteRowCount(t, db, "run", "scheduled-run-no-graph", 0)
	assertDeleteRowCount(t, db, "schedule", "scheduled-run-schedule", 1)
}

func TestDeleteScheduledRun_RequiresScheduleOwner(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForScheduledRunDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})

	err := svc.DeleteScheduledRun(ctx, "scheduled-run-graph")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	assertDeleteRowCount(t, db, "run", "scheduled-run-graph", 1)
}

func TestDeleteScheduledRun_ReturnsNotFound(t *testing.T) {
	svc := newSeededService(t)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	err := svc.DeleteScheduledRun(ctx, "missing-run")
	if !errors.Is(err, ErrScheduledRunNotFound) {
		t.Fatalf("expected ErrScheduledRunNotFound, got %v", err)
	}
}

func TestDeleteScheduledRun_BlocksLiveAndAllowsStaleRun(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		svc, db := newSeededServiceWithDB(t, seedForLiveScheduledRunDelete)
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

		err := svc.DeleteScheduledRun(ctx, "scheduled-run-live")
		if !errors.Is(err, ErrConversationActive) {
			t.Fatalf("expected ErrConversationActive, got %v", err)
		}
		assertDeleteRowCount(t, db, "run", "scheduled-run-live", 1)
	})

	t.Run("stale", func(t *testing.T) {
		svc, db := newSeededServiceWithDB(t, seedForStaleScheduledRunDelete)
		ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

		if err := svc.DeleteScheduledRun(ctx, "scheduled-run-stale"); err != nil {
			t.Fatalf("DeleteScheduledRun() error: %v", err)
		}
		assertDeleteRowCount(t, db, "run", "scheduled-run-stale", 0)
		assertDeleteRowCount(t, db, "schedule", "scheduled-run-stale-schedule", 1)
	})
}

func seedForScheduledRunDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-schedule", "Scheduled Run Delete", "u1", "private", "simple", 1, "adhoc", "UTC", now}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-conversation", now, now, "succeeded", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-child", now, now, "succeeded", "u1", "scheduled-run-conversation"}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-graph", "scheduled-run-schedule", "scheduled-run-conversation", "scheduled", "succeeded", "u1", now, now, now}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-run-no-graph", "scheduled-run-schedule", "scheduled", "succeeded", "u1", now, now, now}},
	})
}

func seedForLiveScheduledRunDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	seedScheduledRunWithoutGraph(t, db, "scheduled-run-live-schedule", "scheduled-run-live", now.Add(time.Minute), now, now)
}

func seedForStaleScheduledRunDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	seedScheduledRunWithoutGraph(t, db, "scheduled-run-stale-schedule", "scheduled-run-stale", now.Add(-time.Hour), now.Add(-time.Hour), now.Add(-time.Hour))
}

func seedScheduledRunWithoutGraph(t *testing.T, db *sql.DB, scheduleID, runID string, leaseUntil, heartbeat, createdAt time.Time) {
	t.Helper()
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{scheduleID, scheduleID, "u1", "private", "simple", 1, "adhoc", "UTC", createdAt.Format(time.RFC3339)}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, lease_until, last_heartbeat_at, heartbeat_interval_sec, created_at, updated_at, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{runID, scheduleID, "scheduled", "running", "u1", leaseUntil.Format(time.RFC3339), heartbeat.Format(time.RFC3339), 5, createdAt.Format(time.RFC3339), createdAt.Format(time.RFC3339), createdAt.Format(time.RFC3339)}},
	})
}

func assertDeleteRowCount(t *testing.T, db *sql.DB, table, id string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("count %s %s: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s %s count=%d, want %d", table, id, got, want)
	}
}
