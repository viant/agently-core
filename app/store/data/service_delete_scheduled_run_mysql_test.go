package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestDeleteScheduledRun_MySQLDeletesCurrentAndLegacyButKeepsScheduleAndNewerRun(t *testing.T) {
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
	scheduleID := "delete-scheduled-schedule-" + suffix
	currentConversationID := "delete-scheduled-current-conversation-" + suffix
	legacyConversationID := "delete-scheduled-legacy-conversation-" + suffix
	newerConversationID := "delete-scheduled-newer-conversation-" + suffix
	currentRunID := "delete-scheduled-current-" + suffix
	legacyRunID := "delete-scheduled-legacy-" + suffix
	newerRunID := "delete-scheduled-newer-" + suffix
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	newer := time.Now().UTC().Truncate(time.Second)

	t.Cleanup(func() {
		for _, runID := range []string{currentRunID, newerRunID} {
			if _, cleanupErr := db.Exec("DELETE FROM run WHERE id = ?", runID); cleanupErr != nil {
				t.Errorf("cleanup run %s: %v", runID, cleanupErr)
			}
		}
		if _, cleanupErr := db.Exec("DELETE FROM schedule_run WHERE id = ?", legacyRunID); cleanupErr != nil {
			t.Errorf("cleanup legacy run: %v", cleanupErr)
		}
		for _, conversationID := range []string{currentConversationID, legacyConversationID, newerConversationID} {
			if _, cleanupErr := db.Exec("DELETE FROM conversation WHERE id = ?", conversationID); cleanupErr != nil {
				t.Errorf("cleanup conversation %s: %v", conversationID, cleanupErr)
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
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{currentConversationID, old, old, old, "succeeded", "owner-1"}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyConversationID, old, old, old, "succeeded", "owner-1", legacyRunID}},
		{query: `INSERT INTO conversation (id, created_at, updated_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{newerConversationID, newer, newer, newer, "succeeded", "owner-1"}},
		{query: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{currentRunID, scheduleID, currentConversationID, "scheduled", "succeeded", "owner-1", old, old, old}},
		{query: `INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{legacyRunID, scheduleID, legacyConversationID, "succeeded", "scheduled", old, old, old}},
		{query: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{newerRunID, scheduleID, newerConversationID, "scheduled", "succeeded", "owner-1", newer, newer, newer}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL scheduled-run deletion test with %q: %v", statement.query, err)
		}
	}

	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner-1"})
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

	if err = service.DeleteScheduledRun(ctx, currentRunID); err != nil {
		t.Fatalf("DeleteScheduledRun(current) error: %v", err)
	}
	assertMySQLScheduledRunDeleteCount(t, db, "schedule", scheduleID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "run", currentRunID, 0)
	assertMySQLScheduledRunDeleteCount(t, db, "conversation", currentConversationID, 0)
	assertMySQLScheduledRunDeleteCount(t, db, "schedule_run", legacyRunID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "conversation", legacyConversationID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "run", newerRunID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "conversation", newerConversationID, 1)

	if err = service.DeleteScheduledRun(ctx, legacyRunID); err != nil {
		t.Fatalf("DeleteScheduledRun(legacy) error: %v", err)
	}
	assertMySQLScheduledRunDeleteCount(t, db, "schedule", scheduleID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "schedule_run", legacyRunID, 0)
	assertMySQLScheduledRunDeleteCount(t, db, "conversation", legacyConversationID, 0)
	assertMySQLScheduledRunDeleteCount(t, db, "run", newerRunID, 1)
	assertMySQLScheduledRunDeleteCount(t, db, "conversation", newerConversationID, 1)
}

func assertMySQLScheduledRunDeleteCount(t *testing.T, db *sql.DB, table, id string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("count %s %s: %v", table, id, err)
	}
	if got != want {
		t.Fatalf("%s %s count=%d, want %d", table, id, got, want)
	}
}
