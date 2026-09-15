package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestListScheduledRunMaintenanceCandidates_UsesStructuralRelationsAndKeyset(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"retention-schedule", "retention-schedule", "owner-1", "agent"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"old-failed", "retention-schedule", "scheduled", "failed", old, old}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"old-success", "retention-schedule", "scheduled", "succeeded", old, old}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"recent-run", "retention-schedule", "scheduled", "succeeded", old, cutoff.Add(time.Second)}},
			{SQL: `INSERT INTO run (id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"not-scheduled", "scheduled", "succeeded", old, old}},
		})
	})

	request := ScheduledRunMaintenanceCandidateRequest{InactiveBefore: cutoff, Limit: 1}
	first, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListScheduledRunMaintenanceCandidates(first) error: %v", err)
	}
	assertScheduledMaintenanceCandidateIDs(t, first, "old-failed")
	if first[0].ExpectedOwnerID != "owner-1" || !first[0].ActivityAt.Equal(old) {
		t.Fatalf("first candidate = %#v", first[0])
	}

	request.AfterActivity = first[0].ActivityAt
	request.AfterRunID = first[0].RunID
	second, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListScheduledRunMaintenanceCandidates(second) error: %v", err)
	}
	assertScheduledMaintenanceCandidateIDs(t, second, "old-success")

	request.AfterActivity = second[0].ActivityAt
	request.AfterRunID = second[0].RunID
	last, err := svc.ListScheduledRunMaintenanceCandidates(context.Background(), request)
	if err != nil {
		t.Fatalf("ListScheduledRunMaintenanceCandidates(last) error: %v", err)
	}
	assertScheduledMaintenanceCandidateIDs(t, last)
}

func TestMaintainScheduledRun_DryRunCoversGraphEmptyLegacyAndLiveness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-60 * 24 * time.Hour)
	cutoff := now.Add(-30 * 24 * time.Hour)
	recent := now.Add(-24 * time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scheduled-retention", "scheduled-retention", "owner-1", "agent"}},
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, internal, agent_ref) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"internal-retention", "internal-retention", "owner-1", 1, "agent"}},

			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-old-root", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-old-child", old, old.Add(time.Hour), "failed", "owner-1", "scheduled-old-root"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-success", "scheduled-retention", "scheduled-old-root", "scheduled", "succeeded", old, old.Add(2 * time.Hour)}},

			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-failed-empty", "scheduled-retention", "scheduled", "failed", old, old.Add(3 * time.Hour)}},

			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-recent-root", old, recent, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-recent-graph", "scheduled-retention", "scheduled-recent-root", "scheduled", "succeeded", old, old}},

			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, lease_until, last_heartbeat_at, heartbeat_interval_sec, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-live", "scheduled-retention", "scheduled", "running", now.Add(time.Minute), now, 5, old}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, lease_until, last_heartbeat_at, heartbeat_interval_sec, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-stale", "scheduled-retention", "scheduled", "running", old, old, 5, old}},

			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-legacy-conversation", old, old.Add(4 * time.Hour), nil, "owner-1", "scheduled-legacy"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, lease_until, last_heartbeat_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-legacy", "scheduled-retention", "interactive", "legacy_unknown", old, old, old}},

			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-internal", "internal-retention", "scheduled", "succeeded", old, old}},
		})
	})

	beforeRuns := tableRowCount(t, db, "run")
	beforeConversations := tableRowCount(t, db, "conversation")
	beforeSchedules := tableRowCount(t, db, "schedule")

	tests := []struct {
		runID             string
		eligible          bool
		reason            ConversationMaintenanceReason
		conversationCount int
		lastActivity      time.Time
	}{
		{runID: "scheduled-success", eligible: true, reason: ConversationMaintenanceEligible, conversationCount: 2, lastActivity: old.Add(2 * time.Hour)},
		{runID: "scheduled-failed-empty", eligible: true, reason: ConversationMaintenanceEligible, lastActivity: old.Add(3 * time.Hour)},
		{runID: "scheduled-recent-graph", reason: ConversationMaintenanceRecentActivity, conversationCount: 1, lastActivity: recent},
		{runID: "scheduled-live", reason: ConversationMaintenanceLiveRun, lastActivity: old},
		{runID: "scheduled-stale", eligible: true, reason: ConversationMaintenanceEligible, lastActivity: old},
		{runID: "scheduled-legacy", eligible: true, reason: ConversationMaintenanceEligible, conversationCount: 1, lastActivity: old.Add(4 * time.Hour)},
		{runID: "scheduled-internal", reason: ConversationMaintenanceInternalSchedule, lastActivity: old},
	}
	for _, test := range tests {
		t.Run(test.runID, func(t *testing.T) {
			result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
				RunID:           test.runID,
				ExpectedOwnerID: "owner-1",
				InactiveBefore:  cutoff,
			})
			if err != nil {
				t.Fatalf("MaintainScheduledRun() error: %v", err)
			}
			if result.Eligible != test.eligible || result.Reason != test.reason || result.ConversationCount != test.conversationCount {
				t.Fatalf("result = %#v", result)
			}
			if !result.LastActivity.Equal(test.lastActivity) {
				t.Fatalf("last activity = %s, want %s; result=%#v", result.LastActivity, test.lastActivity, result)
			}
		})
	}

	if got := tableRowCount(t, db, "run"); got != beforeRuns {
		t.Fatalf("dry-run changed run count: before=%d after=%d", beforeRuns, got)
	}
	if got := tableRowCount(t, db, "conversation"); got != beforeConversations {
		t.Fatalf("dry-run changed conversation count: before=%d after=%d", beforeConversations, got)
	}
	if got := tableRowCount(t, db, "schedule"); got != beforeSchedules {
		t.Fatalf("dry-run changed schedule count: before=%d after=%d", beforeSchedules, got)
	}
}

func TestMaintainScheduledRun_DeleteIsFencedAndKeepsSchedule(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	seed := func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scheduled-delete", "scheduled-delete", "owner-1", "agent"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-delete-root", old, old, "succeeded", "owner-1"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-delete-child", old, old, "failed", "owner-1", "scheduled-delete-root"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-delete-run", "scheduled-delete", "scheduled-delete-root", "scheduled", "succeeded", old, old}},
		})
	}

	t.Run("current lease deletes run and graph", func(t *testing.T) {
		svc, db := newSeededServiceWithDB(t, seed)
		result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
			RunID:           "scheduled-delete-run",
			ExpectedOwnerID: "owner-1",
			InactiveBefore:  cutoff,
			Mode:            ConversationMaintenanceDelete,
			Lease:           acquireTestMaintenanceLease(t, svc),
		})
		if err != nil {
			t.Fatalf("MaintainScheduledRun(delete) error: %v", err)
		}
		if !result.Eligible || !result.Deleted || result.Reason != ConversationMaintenanceDeleted {
			t.Fatalf("delete result = %#v", result)
		}
		assertDeleteRowCount(t, db, "run", "scheduled-delete-run", 0)
		assertDeleteRowCount(t, db, "conversation", "scheduled-delete-root", 0)
		assertDeleteRowCount(t, db, "conversation", "scheduled-delete-child", 0)
		assertDeleteRowCount(t, db, "schedule", "scheduled-delete", 1)
	})

	t.Run("superseded lease cannot delete", func(t *testing.T) {
		svc, db := newSeededServiceWithDB(t, seed)
		stale := acquireTestMaintenanceLease(t, svc, "scheduled-delete-lease", "worker-a")
		released, err := svc.ReleaseMaintenanceLease(context.Background(), stale)
		if err != nil || !released {
			t.Fatalf("release stale lease: released=%t err=%v", released, err)
		}
		_ = acquireTestMaintenanceLease(t, svc, "scheduled-delete-lease", "worker-b")

		_, err = svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
			RunID:           "scheduled-delete-run",
			ExpectedOwnerID: "owner-1",
			InactiveBefore:  cutoff,
			Mode:            ConversationMaintenanceDelete,
			Lease:           stale,
		})
		if !errors.Is(err, ErrMaintenanceLeaseLost) {
			t.Fatalf("stale lease error = %v", err)
		}
		assertDeleteRowCount(t, db, "run", "scheduled-delete-run", 1)
		assertDeleteRowCount(t, db, "conversation", "scheduled-delete-root", 1)
	})
}

func TestMaintainScheduledRun_ExpectedOwnerDoesNotGateSystemRetentionAndValidation(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	svc, _ := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scheduled-owner", "scheduled-owner", "owner-1", "agent"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scheduled-owner-run", "scheduled-owner", "scheduled", "failed", old}},
		})
	})

	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
		RunID:           "scheduled-owner-run",
		ExpectedOwnerID: "owner-2",
		InactiveBefore:  old.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun(expected owner mismatch) error: %v", err)
	}
	if result.Reason != ConversationMaintenanceEligible || !result.Eligible {
		t.Fatalf("expected owner mismatch result = %#v", result)
	}

	_, err = svc.ListScheduledRunMaintenanceCandidates(context.Background(), ScheduledRunMaintenanceCandidateRequest{})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("candidate validation error = %v", err)
	}
	_, err = svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{})
	if !errors.Is(err, ErrInvalidConversationMaintenanceRequest) {
		t.Fatalf("maintenance validation error = %v", err)
	}
}

func TestMaintainScheduledRun_SystemRetentionDeletesGraphRegardlessOfHistoricalOwners(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"system-owner-schedule", "system-owner-schedule", "schedule-owner", "agent"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"system-owner-root", old, old, "succeeded", nil}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"system-owner-child", old, old, "failed", "different-owner", "system-owner-root"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"system-owner-run", "system-owner-schedule", "system-owner-root", "scheduled", "succeeded", "run-owner", old, old}},
		})
	})

	dryRun, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
		RunID:           "system-owner-run",
		ExpectedOwnerID: "stale-candidate-owner",
		InactiveBefore:  cutoff,
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun(dry-run) error: %v", err)
	}
	if !dryRun.Eligible || dryRun.Reason != ConversationMaintenanceEligible || dryRun.ConversationCount != 2 {
		t.Fatalf("dry-run result = %#v", dryRun)
	}

	deleted, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
		RunID:          "system-owner-run",
		InactiveBefore: cutoff,
		Mode:           ConversationMaintenanceDelete,
		Lease:          acquireTestMaintenanceLease(t, svc),
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun(delete) error: %v", err)
	}
	if !deleted.Eligible || !deleted.Deleted || deleted.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("delete result = %#v", deleted)
	}
	assertDeleteRowCount(t, db, "run", "system-owner-run", 0)
	assertDeleteRowCount(t, db, "conversation", "system-owner-root", 0)
	assertDeleteRowCount(t, db, "conversation", "system-owner-child", 0)
	assertDeleteRowCount(t, db, "schedule", "system-owner-schedule", 1)
}

func TestMaintainScheduledRun_BlocksGraphExplicitlyAssignedToAnotherScheduledRun(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scope-target-schedule", "scope-target-schedule", "owner-1", "agent"}},
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scope-other-schedule", "scope-other-schedule", "owner-2", "agent"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"scope-shared-conversation", old, old, "failed", "owner-1"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scope-target-run", "scope-target-schedule", "scope-shared-conversation", "scheduled", "failed", old, old}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scope-other-run", "scope-other-schedule", "scope-shared-conversation", "scheduled", "failed", old, old}},
		})
	})

	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
		RunID:          "scope-target-run",
		InactiveBefore: cutoff,
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun() error: %v", err)
	}
	if result.Eligible || result.Reason != ConversationMaintenanceGraphReferenced {
		t.Fatalf("result = %#v", result)
	}
	assertDeleteRowCount(t, db, "run", "scope-target-run", 1)
	assertDeleteRowCount(t, db, "run", "scope-other-run", 1)
	assertDeleteRowCount(t, db, "conversation", "scope-shared-conversation", 1)
}

func TestMaintainScheduledRun_BlocksConversationMarkedForAnotherScheduledRun(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	cutoff := old.Add(24 * time.Hour)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, agent_ref) VALUES (?, ?, ?, ?)`, Params: []interface{}{"scope-marker-schedule", "scope-marker-schedule", "owner-1", "agent"}},
			{SQL: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scope-marker-conversation", old, old, "failed", "owner-1", "different-run"}},
			{SQL: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, created_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"scope-marker-target-run", "scope-marker-schedule", "scope-marker-conversation", "scheduled", "failed", old, old}},
		})
	})

	result, err := svc.MaintainScheduledRun(context.Background(), ScheduledRunMaintenanceRequest{
		RunID:          "scope-marker-target-run",
		InactiveBefore: cutoff,
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun() error: %v", err)
	}
	if result.Eligible || result.Reason != ConversationMaintenanceGraphReferenced {
		t.Fatalf("result = %#v", result)
	}
	assertDeleteRowCount(t, db, "run", "scope-marker-target-run", 1)
	assertDeleteRowCount(t, db, "conversation", "scope-marker-conversation", 1)
}

func assertScheduledMaintenanceCandidateIDs(t *testing.T, candidates []ScheduledRunMaintenanceCandidate, want ...string) {
	t.Helper()
	if len(candidates) != len(want) {
		t.Fatalf("candidate count = %d, want %d; candidates=%#v", len(candidates), len(want), candidates)
	}
	for index, candidate := range candidates {
		if candidate.RunID != want[index] {
			t.Fatalf("candidate[%d].RunID = %q, want %q; candidates=%#v", index, candidate.RunID, want[index], candidates)
		}
	}
}

func tableRowCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	allowed := map[string]bool{"run": true, "conversation": true, "schedule": true, "schedule_run": true}
	if !allowed[table] {
		t.Fatalf("unsupported count table %q", table)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
