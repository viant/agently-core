package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestMaintainConversationTree_DryRunThenDeleteWholeInteractiveGraph(t *testing.T) {
	rootActivity := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	childActivity := rootActivity.Add(time.Hour)
	cutoff := childActivity.Add(time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-root", rootActivity, rootActivity, rootActivity, "unknown_legacy_state", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-child", rootActivity, childActivity, childActivity, "running", "owner-1", "maintenance-root"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"maintenance-turn", "maintenance-child", "running"}},
			{SQL: `INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-message", "maintenance-child", "maintenance-turn", "tool", "text", "legacy"}},
			{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-message", "maintenance-turn", "maintenance-op", 1, "test/tool", "mcp", "running"}},
		})
	})

	request := ConversationMaintenanceRequest{
		RootID:          "maintenance-root",
		ExpectedOwnerID: "owner-1",
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  cutoff,
		Mode:            ConversationMaintenanceDryRun,
	}
	result, err := svc.MaintainConversationTree(context.Background(), request)
	if err != nil {
		t.Fatalf("MaintainConversationTree(dry-run) error: %v", err)
	}
	if !result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceEligible {
		t.Fatalf("dry-run result = %#v", result)
	}
	if result.ConversationCount != 2 || !result.LastActivity.Equal(childActivity) {
		t.Fatalf("dry-run graph metadata = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-root", 1)
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-child", 1)

	request.Mode = ConversationMaintenanceDelete
	request.Lease = acquireTestMaintenanceLease(t, svc)
	result, err = svc.MaintainConversationTree(context.Background(), request)
	if err != nil {
		t.Fatalf("MaintainConversationTree(delete) error: %v", err)
	}
	if !result.Eligible || !result.Deleted || result.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("delete result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-child", 0)
	assertStage1RowCount(t, db, "tool_call", "message_id", "maintenance-message", 0)
}

func TestMaintainConversationTree_RollsBackDeleteOnFailure(t *testing.T) {
	activityAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `CREATE TABLE investigation (id TEXT PRIMARY KEY, conversation_id TEXT)`},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-rollback", activityAt, activityAt, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"maintenance-rollback-turn", "maintenance-rollback", "succeeded"}},
			{SQL: `INSERT INTO investigation (id, conversation_id) VALUES (?, ?)`, Params: []interface{}{"maintenance-rollback-investigation", "maintenance-rollback"}},
			{SQL: `CREATE TRIGGER fail_maintenance_turn_delete BEFORE DELETE ON turn WHEN OLD.id = 'maintenance-rollback-turn' BEGIN SELECT RAISE(ABORT, 'forced maintenance delete failure'); END`},
		})
	})

	_, err := svc.MaintainConversationTree(context.Background(), ConversationMaintenanceRequest{
		RootID:          "maintenance-rollback",
		ExpectedOwnerID: "owner-1",
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  activityAt.Add(time.Hour),
		Mode:            ConversationMaintenanceDelete,
		Lease:           acquireTestMaintenanceLease(t, svc),
	})
	if err == nil {
		t.Fatal("expected forced maintenance delete failure")
	}
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-rollback", 1)
	assertStage1RowCount(t, db, "turn", "id", "maintenance-rollback-turn", 1)
	var conversationID sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM investigation WHERE id = ?`, "maintenance-rollback-investigation").Scan(&conversationID); err != nil {
		t.Fatalf("query investigation after rollback: %v", err)
	}
	if !conversationID.Valid || conversationID.String != "maintenance-rollback" {
		t.Fatalf("investigation mutation should be rolled back, got %#v", conversationID)
	}
}

func TestMaintainConversationTree_RejectsSupersededLeaseWithoutDeleting(t *testing.T) {
	activityAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"maintenance-fenced", activityAt, activityAt, "succeeded", "owner-1"}},
		})
	})
	stale := acquireTestMaintenanceLease(t, svc, "conversation-fence", "worker-a")
	released, err := svc.ReleaseMaintenanceLease(context.Background(), stale)
	if err != nil || !released {
		t.Fatalf("release stale lease: released=%t err=%v", released, err)
	}
	_ = acquireTestMaintenanceLease(t, svc, "conversation-fence", "worker-b")

	_, err = svc.MaintainConversationTree(context.Background(), ConversationMaintenanceRequest{
		RootID:          "maintenance-fenced",
		ExpectedOwnerID: "owner-1",
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  activityAt.Add(time.Hour),
		Mode:            ConversationMaintenanceDelete,
		Lease:           stale,
	})
	if !errors.Is(err, ErrMaintenanceLeaseLost) {
		t.Fatalf("stale lease error = %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "maintenance-fenced", 1)
}

func TestMaintainConversationTree_SkipsGraphWithRecentChild(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	recent := old.Add(48 * time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"old-root", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"recent-child", old, recent, "succeeded", "owner-1", "old-root"}},
		})
	})

	result := maintainConversationForTest(t, svc, ConversationMaintenanceRequest{
		RootID:          "old-root",
		ExpectedOwnerID: "owner-1",
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  old.Add(24 * time.Hour),
		Mode:            ConversationMaintenanceDelete,
	})
	if result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceRecentActivity || !result.LastActivity.Equal(recent) {
		t.Fatalf("result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "old-root", 1)
	assertStage1RowCount(t, db, "conversation", "id", "recent-child", 1)
}

func TestMaintainConversationTree_DeletesMixedOwnerGraph(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, status, created_by_user_id) VALUES (?, ?, ?, ?)`, Params: []interface{}{"owner-root", old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"other-owner-child", old, "succeeded", "owner-2", "owner-root"}},
		})
	})

	request := maintenanceRequest("owner-root", "owner-1", old.Add(time.Hour))
	request.Mode = ConversationMaintenanceDelete
	result := maintainConversationForTest(t, svc, request)
	if result.Reason != ConversationMaintenanceDeleted || !result.Eligible || !result.Deleted {
		t.Fatalf("result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "owner-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "other-owner-child", 0)
}

func TestMaintainConversationTree_DeletesOwnerlessGraphWithoutExpectedOwner(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"ownerless-root", old, old, "succeeded"}},
		})
	})

	result := maintainConversationForTest(t, svc, ConversationMaintenanceRequest{
		RootID:         "ownerless-root",
		Kind:           ConversationMaintenanceInteractive,
		InactiveBefore: old.Add(time.Hour),
		Mode:           ConversationMaintenanceDelete,
	})
	if result.Reason != ConversationMaintenanceDeleted || !result.Eligible || !result.Deleted {
		t.Fatalf("result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "ownerless-root", 0)
}

func TestMaintainConversationTree_SkipsLiveRun(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	now := time.Now().UTC()
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"live-root", old, old, "running", "owner-1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"live-turn", "live-root", "running"}},
			{SQL: `INSERT INTO run (id, turn_id, conversation_id, conversation_kind, status, lease_until, last_heartbeat_at, heartbeat_interval_sec) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"live-run", "live-turn", "live-root", "interactive", "running", now.Add(time.Minute), now, 5}},
			{SQL: `UPDATE turn SET run_id = ? WHERE id = ?`, Params: []interface{}{"live-run", "live-turn"}},
		})
	})

	request := maintenanceRequest("live-root", "owner-1", old.Add(time.Hour))
	request.Mode = ConversationMaintenanceDelete
	result := maintainConversationForTest(t, svc, request)
	if result.Reason != ConversationMaintenanceLiveRun || result.Eligible || result.Deleted {
		t.Fatalf("result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "live-root", 1)
}

func TestMaintainConversationTree_VerifiesKindAndRoot(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-root", old, old, "succeeded", "owner-1", "schedule-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-child", old, old, "succeeded", "owner-1", "scheduled-root"}},
		})
	})

	request := maintenanceRequest("scheduled-root", "owner-1", old.Add(time.Hour))
	result := maintainConversationForTest(t, svc, request)
	if result.Reason != ConversationMaintenanceKindMismatch {
		t.Fatalf("interactive-kind result = %#v", result)
	}

	request.Kind = ConversationMaintenanceScheduled
	result = maintainConversationForTest(t, svc, request)
	if result.Reason != ConversationMaintenanceEligible || !result.Eligible {
		t.Fatalf("scheduled-kind result = %#v", result)
	}

	request.RootID = "scheduled-child"
	result = maintainConversationForTest(t, svc, request)
	if result.Reason != ConversationMaintenanceNotRoot || result.Eligible {
		t.Fatalf("child-root result = %#v", result)
	}
}

func TestMaintainConversationTree_ScheduledFallbackDeletesOwnerlessShellAndPreservesSchedule(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"fallback-schedule", "fallback-schedule", "schedule-owner", "agent"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, schedule_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"fallback-shell", old, old, "succeeded", "fallback-schedule"}},
		})
	})

	request := ConversationMaintenanceRequest{
		RootID:         "fallback-shell",
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: old.Add(time.Hour),
		Mode:           ConversationMaintenanceDryRun,
	}
	result := maintainConversationForTest(t, svc, request)
	if !result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceEligible {
		t.Fatalf("dry-run result = %#v", result)
	}

	request.Mode = ConversationMaintenanceDelete
	result = maintainConversationForTest(t, svc, request)
	if !result.Eligible || !result.Deleted || result.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("delete result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "fallback-shell", 0)
	assertStage1RowCount(t, db, "schedule", "id", "fallback-schedule", 1)
}

func TestMaintainConversationTree_ScheduledFallbackRechecksWholeGraphForRuns(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, schedule_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"fallback-root", old, old, "succeeded", "schedule-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, conversation_parent_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"fallback-child", old, old, "succeeded", "fallback-root"}},
			{SQL: `INSERT INTO run (id, conversation_id, conversation_kind, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"child-run", "fallback-child", "scheduled", "completed"}},
		})
	})

	result := maintainConversationForTest(t, svc, ConversationMaintenanceRequest{
		RootID:         "fallback-root",
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: old.Add(time.Hour),
		Mode:           ConversationMaintenanceDelete,
	})
	if result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceRunPresent {
		t.Fatalf("result = %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", "fallback-root", 1)
	assertStage1RowCount(t, db, "conversation", "id", "fallback-child", 1)
	assertStage1RowCount(t, db, "run", "id", "child-run", 1)
}

func TestMaintainConversationTree_SkipsInteractiveGraphWithScheduledDescendant(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"interactive-root", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id, schedule_kind) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-descendant", old, old, "succeeded", "owner-1", "interactive-root", "cron"}},
		})
	})

	result := maintainConversationForTest(t, svc, maintenanceRequest("interactive-root", "owner-1", old.Add(time.Hour)))
	if result.Reason != ConversationMaintenanceKindMismatch || result.Eligible || result.Deleted {
		t.Fatalf("mixed-kind result = %#v", result)
	}
}

func TestMaintainConversationTree_RequiresExplicitSafetyInputs(t *testing.T) {
	svc := newSeededService(t)
	_, err := svc.MaintainConversationTree(context.Background(), ConversationMaintenanceRequest{})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("expected ErrInvalidConversationMaintenanceRequest, got %v", err)
	}
}

func maintenanceRequest(rootID, ownerID string, cutoff time.Time) ConversationMaintenanceRequest {
	return ConversationMaintenanceRequest{
		RootID:          rootID,
		ExpectedOwnerID: ownerID,
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  cutoff,
		Mode:            ConversationMaintenanceDryRun,
	}
}

func maintainConversationForTest(t *testing.T, svc Service, request ConversationMaintenanceRequest) *ConversationMaintenanceResult {
	t.Helper()
	if request.Mode == ConversationMaintenanceDelete && request.Lease.Token == "" {
		request.Lease = acquireTestMaintenanceLease(t, svc)
	}
	result, err := svc.MaintainConversationTree(context.Background(), request)
	if err != nil {
		t.Fatalf("MaintainConversationTree() error: %v", err)
	}
	return result
}
