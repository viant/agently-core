package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestMaintainScheduledRun_MySQLDryRunCurrentAndLegacyThenDelete(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	scheduleID := "maintenance-scheduled-schedule-" + suffix
	conversationID := "maintenance-scheduled-conversation-" + suffix
	legacyConversationID := "maintenance-scheduled-legacy-conversation-" + suffix
	endedRunID := "maintenance-scheduled-ended-" + suffix
	failedRunID := "maintenance-scheduled-failed-" + suffix
	liveRunID := "maintenance-scheduled-live-" + suffix
	legacyRunID := "maintenance-scheduled-legacy-" + suffix
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)

	t.Cleanup(func() {
		if _, cleanupErr := db.Exec("DELETE FROM schedule_run WHERE id = ?", legacyRunID); cleanupErr != nil {
			t.Errorf("cleanup legacy scheduled run: %v", cleanupErr)
		}
		for _, runID := range []string{endedRunID, failedRunID, liveRunID} {
			if _, cleanupErr := db.Exec("DELETE FROM run WHERE id = ?", runID); cleanupErr != nil {
				t.Errorf("cleanup scheduled run %s: %v", runID, cleanupErr)
			}
		}
		for _, candidateConversationID := range []string{conversationID, legacyConversationID} {
			if _, cleanupErr := db.Exec("DELETE FROM conversation WHERE id = ?", candidateConversationID); cleanupErr != nil {
				t.Errorf("cleanup scheduled conversation %s: %v", candidateConversationID, cleanupErr)
			}
		}
		if _, cleanupErr := db.Exec("DELETE FROM schedule WHERE id = ?", scheduleID); cleanupErr != nil {
			t.Errorf("cleanup schedule: %v", cleanupErr)
		}
	})

	statements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO schedule (id, name, created_by_user_id, internal, visibility, agent_ref, enabled, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{scheduleID, scheduleID, "owner-1", 0, "private", "agent", 1, "adhoc", "UTC"}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{conversationID, old, old.Add(3 * time.Second), old.Add(3 * time.Second), "succeeded", "different-owner"}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyConversationID, old, old.Add(3 * time.Second), old.Add(3 * time.Second), "succeeded", nil, legacyRunID}},
		{query: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{endedRunID, scheduleID, conversationID, "scheduled", "succeeded", "owner-1", old, old, old}},
		{query: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{failedRunID, scheduleID, "scheduled", "failed", "owner-1", old, old.Add(time.Second)}},
		{query: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, lease_until, last_heartbeat_at, heartbeat_interval_sec, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{liveRunID, scheduleID, "scheduled", "running", "owner-1", time.Now().UTC().Add(time.Minute), time.Now().UTC(), 5, old, old}},
		{query: `INSERT INTO schedule_run (id, schedule_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyRunID, scheduleID, "succeeded", "scheduled", old, old.Add(2 * time.Second), old.Add(2 * time.Second)}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL scheduled maintenance test with %q: %v", statement.query, err)
		}
	}

	before := map[string]int{
		"schedule":     scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule WHERE id = ?`, scheduleID),
		"conversation": scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM conversation WHERE id IN (?, ?)`, conversationID, legacyConversationID),
		"run":          scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM run WHERE schedule_id = ?`, scheduleID),
		"schedule_run": scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule_run WHERE schedule_id = ?`, scheduleID),
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}
	service := NewService(dao)

	candidates, err := service.ListScheduledRunMaintenanceCandidates(ctx, ScheduledRunMaintenanceCandidateRequest{
		InactiveBefore: old.Add(4 * time.Second),
		AfterActivity:  old.Add(-time.Second),
		AfterRunID:     "cursor",
		Limit:          100,
	})
	if err != nil {
		t.Fatalf("ListScheduledRunMaintenanceCandidates() on MySQL: %v", err)
	}
	wantCandidates := map[string]bool{
		endedRunID:  false,
		failedRunID: false,
		liveRunID:   false,
		legacyRunID: false,
	}
	for _, candidate := range candidates {
		if _, ok := wantCandidates[candidate.RunID]; !ok {
			continue
		}
		wantCandidates[candidate.RunID] = true
		if candidate.ExpectedOwnerID != "owner-1" {
			t.Fatalf("candidate owner = %q, want owner-1; candidate=%#v", candidate.ExpectedOwnerID, candidate)
		}
	}
	for runID, found := range wantCandidates {
		if !found {
			t.Fatalf("MySQL candidates do not contain %q: %#v", runID, candidates)
		}
	}

	tests := []struct {
		runID             string
		source            ScheduledRunMaintenanceSource
		eligible          bool
		reason            ConversationMaintenanceReason
		conversationCount int
	}{
		{runID: endedRunID, source: ScheduledRunMaintenanceCurrent, eligible: true, reason: ConversationMaintenanceEligible, conversationCount: 1},
		{runID: failedRunID, source: ScheduledRunMaintenanceCurrent, eligible: true, reason: ConversationMaintenanceEligible},
		{runID: liveRunID, source: ScheduledRunMaintenanceCurrent, reason: ConversationMaintenanceLiveRun},
		{runID: legacyRunID, source: ScheduledRunMaintenanceLegacy, eligible: true, reason: ConversationMaintenanceEligible, conversationCount: 1},
	}
	for _, test := range tests {
		result, err := service.MaintainScheduledRun(ctx, ScheduledRunMaintenanceRequest{
			RunID:           test.runID,
			ExpectedOwnerID: "owner-1",
			InactiveBefore:  cutoff,
		})
		if err != nil {
			t.Fatalf("MaintainScheduledRun(%s) on MySQL: %v", test.runID, err)
		}
		if result.Source != test.source || result.Eligible != test.eligible || result.Reason != test.reason || result.ConversationCount != test.conversationCount {
			t.Fatalf("MaintainScheduledRun(%s) result = %#v", test.runID, result)
		}
	}

	after := map[string]int{
		"schedule":     scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule WHERE id = ?`, scheduleID),
		"conversation": scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM conversation WHERE id IN (?, ?)`, conversationID, legacyConversationID),
		"run":          scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM run WHERE schedule_id = ?`, scheduleID),
		"schedule_run": scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule_run WHERE schedule_id = ?`, scheduleID),
	}
	for table, count := range before {
		if got := after[table]; got != count {
			t.Fatalf("dry-run changed %s count: before=%d after=%d", table, count, got)
		}
	}

	leaseKey := "test-scheduled-maintenance-" + suffix
	lease := acquireTestMaintenanceLease(t, service, leaseKey, "test-worker-"+suffix)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey) })
	deleted, err := service.MaintainScheduledRun(ctx, ScheduledRunMaintenanceRequest{
		RunID:           legacyRunID,
		ExpectedOwnerID: "stale-candidate-owner",
		InactiveBefore:  cutoff,
		Mode:            ConversationMaintenanceDelete,
		Lease:           lease,
	})
	if err != nil {
		t.Fatalf("MaintainScheduledRun(delete) on MySQL: %v", err)
	}
	if !deleted.Eligible || !deleted.Deleted || deleted.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("MaintainScheduledRun(delete) result = %#v", deleted)
	}
	if got := scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule_run WHERE id = ?`, legacyRunID); got != 0 {
		t.Fatalf("deleted legacy run count = %d, want 0", got)
	}
	if got := scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM conversation WHERE id = ?`, legacyConversationID); got != 0 {
		t.Fatalf("deleted legacy conversation count = %d, want 0", got)
	}
	if got := scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule WHERE id = ?`, scheduleID); got != 1 {
		t.Fatalf("schedule count after run delete = %d, want 1", got)
	}
}

func TestMaintainConversationTree_ScheduledFallbackMySQL(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	scheduleID := "maintenance-fallback-schedule-" + suffix
	shellID := "maintenance-fallback-shell-" + suffix
	currentRunConversationID := "maintenance-fallback-current-conversation-" + suffix
	legacyRunConversationID := "maintenance-fallback-legacy-conversation-" + suffix
	currentRunID := "maintenance-fallback-current-run-" + suffix
	legacyRunID := "maintenance-fallback-legacy-run-" + suffix
	leaseKey := "test-conversation-fallback-" + suffix
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)

	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey)
		_, _ = db.Exec(`DELETE FROM run WHERE id = ?`, currentRunID)
		_, _ = db.Exec(`DELETE FROM schedule_run WHERE id = ?`, legacyRunID)
		for _, conversationID := range []string{shellID, currentRunConversationID, legacyRunConversationID} {
			_, _ = db.Exec(`DELETE FROM conversation WHERE id = ?`, conversationID)
		}
		_, _ = db.Exec(`DELETE FROM schedule WHERE id = ?`, scheduleID)
	})

	statements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO schedule (id, name, created_by_user_id, internal, visibility, agent_ref, enabled, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{scheduleID, scheduleID, "owner-1", 0, "private", "agent", 1, "adhoc", "UTC"}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{shellID, old, old, old, "succeeded", nil, scheduleID}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{currentRunConversationID, old, old, old, "succeeded", "owner-1", scheduleID}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, schedule_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyRunConversationID, old, old, old, "succeeded", "owner-1", scheduleID, legacyRunID}},
		{query: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{currentRunID, scheduleID, currentRunConversationID, "scheduled", "succeeded", "owner-1", old, old, old}},
		{query: `INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyRunID, scheduleID, legacyRunConversationID, "succeeded", "scheduled", old, old, old}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL scheduled fallback test with %q: %v", statement.query, err)
		}
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}
	service := NewService(dao)

	candidates, err := service.ListConversationMaintenanceCandidates(ctx, ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: cutoff,
		Limit:          100,
	})
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates(scheduled fallback) on MySQL: %v", err)
	}
	found := map[string]bool{
		shellID:                  false,
		currentRunConversationID: false,
		legacyRunConversationID:  false,
	}
	for _, candidate := range candidates {
		if _, ok := found[candidate.RootID]; ok {
			found[candidate.RootID] = true
		}
	}
	if !found[shellID] || found[currentRunConversationID] || found[legacyRunConversationID] {
		t.Fatalf("scheduled fallback candidates include unexpected fixtures: found=%v candidates=%#v", found, candidates)
	}

	blocked, err := service.MaintainConversationTree(ctx, ConversationMaintenanceRequest{
		RootID:         legacyRunConversationID,
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: cutoff,
		Mode:           ConversationMaintenanceDryRun,
	})
	if err != nil {
		t.Fatalf("MaintainConversationTree(blocked scheduled fallback) on MySQL: %v", err)
	}
	if blocked.Eligible || blocked.Deleted || blocked.Reason != ConversationMaintenanceRunPresent {
		t.Fatalf("blocked scheduled fallback result = %#v", blocked)
	}

	lease := acquireTestMaintenanceLease(t, service, leaseKey, "test-worker-"+suffix)
	deleted, err := service.MaintainConversationTree(ctx, ConversationMaintenanceRequest{
		RootID:         shellID,
		Kind:           ConversationMaintenanceScheduledFallback,
		InactiveBefore: cutoff,
		Mode:           ConversationMaintenanceDelete,
		Lease:          lease,
	})
	if err != nil {
		t.Fatalf("MaintainConversationTree(delete scheduled fallback) on MySQL: %v", err)
	}
	if !deleted.Eligible || !deleted.Deleted || deleted.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("deleted scheduled fallback result = %#v", deleted)
	}
	if got := scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM conversation WHERE id = ?`, shellID); got != 0 {
		t.Fatalf("deleted shell count = %d, want 0", got)
	}
	if got := scheduledMaintenanceFixtureCount(t, db, `SELECT COUNT(*) FROM schedule WHERE id = ?`, scheduleID); got != 1 {
		t.Fatalf("schedule count after fallback delete = %d, want 1", got)
	}
}

func scheduledMaintenanceFixtureCount(t *testing.T, db *sql.DB, query string, args ...interface{}) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count scheduled maintenance fixture rows with %q: %v", query, err)
	}
	return count
}
