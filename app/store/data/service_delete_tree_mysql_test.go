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

func TestDeleteConversationTree_MySQLStage1(t *testing.T) {
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
	conversationID := "delete-mysql-conv-" + suffix
	turnID := "delete-mysql-turn-" + suffix
	messageID := "delete-mysql-msg-" + suffix
	payloadID := "delete-mysql-payload-" + suffix
	goalID := "delete-mysql-goal-" + suffix
	scheduleID := "goal-wakeup-" + goalID
	runID := "delete-mysql-run-" + suffix
	investigationID := "delete-mysql-investigation-" + suffix

	t.Cleanup(func() {
		cleanupMySQLDeleteTestRows(t, db, map[string]string{
			"conversation":  conversationID,
			"turn":          turnID,
			"message":       messageID,
			"payload":       payloadID,
			"goal":          goalID,
			"schedule":      scheduleID,
			"run":           runID,
			"investigation": investigationID,
		})
	})

	statements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, args: []interface{}{conversationID, "succeeded", "u1"}},
		{query: `INSERT INTO goal (id, conversation_id, objective, status) VALUES (?, ?, ?, ?)`, args: []interface{}{goalID, conversationID, "finish", "complete"}},
		{query: `INSERT INTO turn (id, conversation_id, goal_id, status) VALUES (?, ?, ?, ?)`, args: []interface{}{turnID, conversationID, goalID, "succeeded"}},
		{query: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{payloadID, "attachment", "text/plain", 4, "object", "external://delete-test-object", "none"}},
		{query: `INSERT INTO message (id, conversation_id, turn_id, role, type, content, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{messageID, conversationID, turnID, "assistant", "text", "test", payloadID}},
		{query: `INSERT INTO schedule (id, name, created_by_user_id, internal, conversation_id, goal_id, agent_ref, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{scheduleID, "autonomous::goal-wakeup::" + goalID, "u1", 1, conversationID, goalID, "agent", "adhoc", "UTC"}},
		{query: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, conversation_kind, status, completed_at) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())`, args: []interface{}{runID, turnID, scheduleID, conversationID, "interactive", "succeeded"}},
		{query: `UPDATE turn SET run_id = ? WHERE id = ?`, args: []interface{}{runID, turnID}},
		{query: `INSERT INTO investigation (id, title, created_by, conversation_id) VALUES (?, ?, ?, ?)`, args: []interface{}{investigationID, "retained", "u1", conversationID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL delete test with %q: %v", statement.query, err)
		}
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	connector := view.NewConnector("agently", "mysql", dsn)
	if err = dao.AddConnectors(ctx, connector); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}

	if err := NewService(dao).DeleteConversationTree(deleteTestContext(), conversationID); err != nil {
		t.Fatalf("DeleteConversationTree() on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", conversationID, 0)
	assertStage1RowCount(t, db, "goal", "id", goalID, 0)
	assertStage1RowCount(t, db, "schedule", "id", scheduleID, 0)
	assertStage1RowCount(t, db, "run", "id", runID, 0)
	assertStage1RowCount(t, db, "call_payload", "id", payloadID, 0)

	var investigationConversationID sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM investigation WHERE id = ?`, investigationID).Scan(&investigationConversationID); err != nil {
		t.Fatalf("query retained MySQL investigation: %v", err)
	}
	if investigationConversationID.Valid {
		t.Fatalf("MySQL investigation conversation_id should be detached, got %q", investigationConversationID.String)
	}
}

func TestDeleteConversationTree_MySQLLegacyNullStatusWithStaleRun(t *testing.T) {
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
	conversationID := "delete-mysql-stale-conv-" + suffix
	turnID := "delete-mysql-stale-turn-" + suffix
	messageID := "delete-mysql-stale-msg-" + suffix
	runID := "delete-mysql-stale-run-" + suffix

	t.Cleanup(func() {
		cleanupMySQLDeleteTestRows(t, db, map[string]string{
			"conversation": conversationID,
			"turn":         turnID,
			"message":      messageID,
			"run":          runID,
		})
	})

	statements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, args: []interface{}{conversationID, nil, "u1"}},
		{query: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, args: []interface{}{turnID, conversationID, "running"}},
		{query: `INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{messageID, conversationID, turnID, "assistant", "text", "stale"}},
		{query: `INSERT INTO run (id, turn_id, conversation_id, conversation_kind, status, lease_until, last_heartbeat_at, heartbeat_interval_sec) VALUES (?, ?, ?, ?, ?, DATE_SUB(UTC_TIMESTAMP(), INTERVAL 10 MINUTE), DATE_SUB(UTC_TIMESTAMP(), INTERVAL 10 MINUTE), ?)`, args: []interface{}{runID, turnID, conversationID, "interactive", "running", 60}},
		{query: `UPDATE turn SET run_id = ? WHERE id = ?`, args: []interface{}{runID, turnID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL stale-running delete test with %q: %v", statement.query, err)
		}
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	connector := view.NewConnector("agently", "mysql", dsn)
	if err = dao.AddConnectors(ctx, connector); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}

	if err := NewService(dao).DeleteConversationTree(deleteTestContext(), conversationID); err != nil {
		t.Fatalf("DeleteConversationTree() on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", conversationID, 0)
}

func cleanupMySQLDeleteTestRows(t *testing.T, db *sql.DB, ids map[string]string) {
	t.Helper()
	statements := []struct {
		query string
		id    string
	}{
		{query: `DELETE FROM investigation WHERE id = ?`, id: ids["investigation"]},
		{query: `DELETE FROM model_call WHERE message_id = ?`, id: ids["message"]},
		{query: `DELETE FROM tool_call WHERE message_id = ?`, id: ids["message"]},
		{query: `DELETE FROM generated_file WHERE conversation_id = ?`, id: ids["conversation"]},
		{query: `DELETE FROM turn_queue WHERE conversation_id = ?`, id: ids["conversation"]},
		{query: `DELETE FROM message WHERE conversation_id = ?`, id: ids["conversation"]},
		{query: `UPDATE turn SET run_id = NULL WHERE id = ?`, id: ids["turn"]},
		{query: `DELETE FROM run WHERE id = ?`, id: ids["run"]},
		{query: `DELETE FROM turn WHERE id = ?`, id: ids["turn"]},
		{query: `DELETE FROM schedule WHERE id = ?`, id: ids["schedule"]},
		{query: `DELETE FROM goal WHERE id = ?`, id: ids["goal"]},
		{query: `DELETE FROM conversation WHERE id = ?`, id: ids["conversation"]},
		{query: `DELETE FROM call_payload WHERE id = ?`, id: ids["payload"]},
	}
	for _, statement := range statements {
		if statement.id == "" {
			continue
		}
		if _, err := db.Exec(statement.query, statement.id); err != nil {
			t.Errorf("cleanup MySQL delete test with %q: %v", statement.query, err)
		}
	}
}
