package data

import (
	"context"
	"database/sql"
	"errors"
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
	reportRunID := "delete-mysql-report-run-" + suffix
	reportJobID := "delete-mysql-report-job-" + suffix
	reportArtifactID := "delete-mysql-report-artifact-" + suffix
	reportAuditID := "delete-mysql-report-audit-" + suffix
	sharedReportID := "delete-mysql-shared-report-" + suffix
	sharedAuditID := "delete-mysql-shared-audit-" + suffix

	t.Cleanup(func() {
		cleanupMySQLDeleteTestRows(t, db, map[string]string{
			"conversation":    conversationID,
			"turn":            turnID,
			"message":         messageID,
			"payload":         payloadID,
			"goal":            goalID,
			"schedule":        scheduleID,
			"run":             runID,
			"investigation":   investigationID,
			"report_run":      reportRunID,
			"report_job":      reportJobID,
			"report_artifact": reportArtifactID,
			"report_audit":    reportAuditID,
			"shared_report":   sharedReportID,
			"shared_audit":    sharedAuditID,
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
		{query: `INSERT INTO investigation (id, title, created_by, conversation_id) VALUES (?, ?, ?, ?)`, args: []interface{}{investigationID, "deleted with conversation", "u1", conversationID}},
		{query: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP(), ?, ?)`, args: []interface{}{reportRunID, "u1", conversationID, "test", "completed", 1, "request-" + reportRunID}},
		{query: `INSERT INTO conversation_report_context (owner_id, conversation_id, active_report_run_id, revision) VALUES (?, ?, ?, ?)`, args: []interface{}{"u1", conversationID, reportRunID, 1}},
		{query: `INSERT INTO report_export_job (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, report_run_id, report_run_revision, export_request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{reportJobID, "external://report", "u1", conversationID, "pdf", "draft", "succeeded", reportRunID, 1, "export-" + reportJobID}},
		{query: `INSERT INTO report_export_artifact (artifact_id, job_id, artifact_ref, owner_id, format, content_type) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{reportArtifactID, reportJobID, "external://report.pdf", "u1", "pdf", "application/pdf"}},
		{query: `INSERT INTO report_audit_event (event_id, event_type, artifact_ref, job_id, artifact_id, actor_id) VALUES (?, ?, ?, ?, ?, ?)`, args: []interface{}{reportAuditID, "export", "external://report.pdf", reportJobID, reportArtifactID, "u1"}},
		{query: `INSERT INTO report_shared_artifact (artifact_id, artifact_ref, owner_id, kind, lifecycle) VALUES (?, ?, ?, ?, ?)`, args: []interface{}{sharedReportID, "saved://report", "u1", "report", "saved"}},
		{query: `INSERT INTO report_audit_event (event_id, event_type, artifact_ref, artifact_id, actor_id) VALUES (?, ?, ?, ?, ?)`, args: []interface{}{sharedAuditID, "saved", "saved://report", sharedReportID, "u1"}},
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
	assertStage1RowCount(t, db, "report_run", "report_run_id", reportRunID, 0)
	assertStage1RowCount(t, db, "report_export_job", "job_id", reportJobID, 0)
	assertStage1RowCount(t, db, "report_export_artifact", "artifact_id", reportArtifactID, 0)
	assertStage1RowCount(t, db, "report_audit_event", "event_id", reportAuditID, 0)
	assertStage1RowCount(t, db, "report_shared_artifact", "artifact_id", sharedReportID, 1)
	assertStage1RowCount(t, db, "report_audit_event", "event_id", sharedAuditID, 1)
	assertStage1RowCount(t, db, "investigation", "id", investigationID, 0)
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

func TestDeleteScheduledRun_MySQL(t *testing.T) {
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
	scheduleID := "delete-scheduled-run-schedule-" + suffix
	conversationID := "delete-scheduled-run-conversation-" + suffix
	graphRunID := "delete-scheduled-run-graph-" + suffix
	emptyRunID := "delete-scheduled-run-empty-" + suffix
	liveRunID := "delete-scheduled-run-live-" + suffix
	t.Cleanup(func() {
		for _, runID := range []string{graphRunID, emptyRunID, liveRunID} {
			if _, cleanupErr := db.Exec("DELETE FROM run WHERE id = ?", runID); cleanupErr != nil {
				t.Errorf("cleanup MySQL scheduled run %s: %v", runID, cleanupErr)
			}
		}
		if _, cleanupErr := db.Exec("DELETE FROM conversation WHERE id = ?", conversationID); cleanupErr != nil {
			t.Errorf("cleanup MySQL scheduled conversation: %v", cleanupErr)
		}
		if _, cleanupErr := db.Exec("DELETE FROM schedule WHERE id = ?", scheduleID); cleanupErr != nil {
			t.Errorf("cleanup MySQL scheduled definition: %v", cleanupErr)
		}
	})

	statements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO schedule (id, name, created_by_user_id, internal, visibility, agent_ref, enabled, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, args: []interface{}{scheduleID, scheduleID, "u1", 0, "private", "agent", 1, "adhoc", "UTC"}},
		{query: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, args: []interface{}{conversationID, "succeeded", "u1"}},
		{query: `INSERT INTO run (id, schedule_id, conversation_id, conversation_kind, status, effective_user_id, completed_at) VALUES (?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())`, args: []interface{}{graphRunID, scheduleID, conversationID, "scheduled", "succeeded", "u1"}},
		{query: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, completed_at) VALUES (?, ?, ?, ?, ?, UTC_TIMESTAMP())`, args: []interface{}{emptyRunID, scheduleID, "scheduled", "succeeded", "u1"}},
		{query: `INSERT INTO run (id, schedule_id, conversation_kind, status, effective_user_id, lease_until, last_heartbeat_at, heartbeat_interval_sec) VALUES (?, ?, ?, ?, ?, DATE_ADD(UTC_TIMESTAMP(), INTERVAL 1 MINUTE), UTC_TIMESTAMP(), ?)`, args: []interface{}{liveRunID, scheduleID, "scheduled", "running", "u1", 5}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL scheduled-run delete test with %q: %v", statement.query, err)
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
	deleteCtx := deleteTestContext()

	if err := service.DeleteScheduledRun(deleteCtx, graphRunID); err != nil {
		t.Fatalf("DeleteScheduledRun(graph) on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", conversationID, 0)
	assertStage1RowCount(t, db, "run", "id", graphRunID, 0)
	assertStage1RowCount(t, db, "schedule", "id", scheduleID, 1)
	if err := service.DeleteScheduledRun(deleteCtx, emptyRunID); err != nil {
		t.Fatalf("DeleteScheduledRun(no graph) on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "run", "id", emptyRunID, 0)

	if err := service.DeleteScheduledRun(deleteCtx, liveRunID); !errors.Is(err, ErrConversationActive) {
		t.Fatalf("DeleteScheduledRun(live) error=%v, want %v", err, ErrConversationActive)
	}
	if _, err := db.Exec(`UPDATE run SET lease_until = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 1 MINUTE), last_heartbeat_at = DATE_SUB(UTC_TIMESTAMP(), INTERVAL 1 MINUTE) WHERE id = ?`, liveRunID); err != nil {
		t.Fatalf("make MySQL run stale: %v", err)
	}
	if err := service.DeleteScheduledRun(deleteCtx, liveRunID); err != nil {
		t.Fatalf("DeleteScheduledRun(stale) on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "run", "id", liveRunID, 0)
	assertStage1RowCount(t, db, "schedule", "id", scheduleID, 1)

	if _, err := db.Exec(`UPDATE schedule SET lease_until = DATE_ADD(UTC_TIMESTAMP(), INTERVAL 1 MINUTE) WHERE id = ?`, scheduleID); err != nil {
		t.Fatalf("claim MySQL schedule before cascade: %v", err)
	}
	if err := service.DeleteScheduleCascade(deleteCtx, scheduleID); !errors.Is(err, ErrConversationActive) {
		t.Fatalf("DeleteScheduleCascade(claimed) error=%v, want %v", err, ErrConversationActive)
	}
	assertStage1RowCount(t, db, "schedule", "id", scheduleID, 1)
	if _, err := db.Exec(`UPDATE schedule SET lease_until = NULL WHERE id = ?`, scheduleID); err != nil {
		t.Fatalf("release MySQL schedule claim: %v", err)
	}
	if err := service.DeleteScheduleCascade(deleteCtx, scheduleID); err != nil {
		t.Fatalf("DeleteScheduleCascade() on MySQL: %v", err)
	}
	assertStage1RowCount(t, db, "schedule", "id", scheduleID, 0)
}

func TestMaintainConversationTree_MySQL(t *testing.T) {
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
	conversationID := "maintenance-mysql-conv-" + suffix
	activityAt := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Second)
	t.Cleanup(func() {
		cleanupMySQLDeleteTestRows(t, db, map[string]string{"conversation": conversationID})
	})
	if _, err := db.Exec(`INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id)
VALUES (?, ?, ?, ?, ?)`, conversationID, activityAt, activityAt, "unknown_legacy_state", "u1"); err != nil {
		t.Fatalf("seed MySQL maintenance test: %v", err)
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

	service := NewService(dao)
	leaseKey := "test-conversation-maintenance-" + suffix
	requestLease := acquireTestMaintenanceLease(t, service, leaseKey, "test-worker-"+suffix)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey) })
	candidates, err := service.ListConversationMaintenanceCandidates(ctx, ConversationMaintenanceCandidateRequest{
		Kind:           ConversationMaintenanceInteractive,
		InactiveBefore: activityAt.Add(time.Second),
		AfterActivity:  activityAt.Add(-time.Second),
		AfterRootID:    "cursor",
		Limit:          100,
	})
	if err != nil {
		t.Fatalf("ListConversationMaintenanceCandidates() on MySQL: %v", err)
	}
	foundCandidate := false
	for _, candidate := range candidates {
		if candidate.RootID == conversationID {
			foundCandidate = true
			if candidate.ExpectedOwnerID != "u1" || !candidate.ActivityAt.Equal(activityAt) {
				t.Fatalf("unexpected MySQL maintenance candidate: %#v", candidate)
			}
			break
		}
	}
	if !foundCandidate {
		t.Fatalf("MySQL maintenance candidates do not contain %q: %#v", conversationID, candidates)
	}

	request := ConversationMaintenanceRequest{
		RootID:          conversationID,
		ExpectedOwnerID: "u1",
		Kind:            ConversationMaintenanceInteractive,
		InactiveBefore:  time.Now().UTC().Add(-30 * 24 * time.Hour),
		Mode:            ConversationMaintenanceDryRun,
	}
	result, err := service.MaintainConversationTree(context.Background(), request)
	if err != nil {
		t.Fatalf("MaintainConversationTree(dry-run) on MySQL: %v", err)
	}
	if !result.Eligible || result.Deleted || result.Reason != ConversationMaintenanceEligible {
		t.Fatalf("MaintainConversationTree(dry-run) result: %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", conversationID, 1)

	request.Mode = ConversationMaintenanceDelete
	request.Lease = requestLease
	result, err = service.MaintainConversationTree(context.Background(), request)
	if err != nil {
		t.Fatalf("MaintainConversationTree(delete) on MySQL: %v", err)
	}
	if !result.Eligible || !result.Deleted || result.Reason != ConversationMaintenanceDeleted {
		t.Fatalf("MaintainConversationTree(delete) result: %#v", result)
	}
	assertStage1RowCount(t, db, "conversation", "id", conversationID, 0)

	rollbackConversationID := "maintenance-mysql-rollback-conv-" + suffix
	rollbackTurnID := "maintenance-mysql-rollback-turn-" + suffix
	rollbackInvestigationID := "maintenance-mysql-rollback-investigation-" + suffix
	guardTable := "maintenance_delete_guard_" + suffix
	t.Cleanup(func() {
		if _, cleanupErr := db.Exec("DROP TABLE IF EXISTS `" + guardTable + "`"); cleanupErr != nil {
			t.Errorf("drop MySQL maintenance rollback guard: %v", cleanupErr)
		}
		cleanupMySQLDeleteTestRows(t, db, map[string]string{
			"conversation":  rollbackConversationID,
			"turn":          rollbackTurnID,
			"investigation": rollbackInvestigationID,
		})
	})
	rollbackStatements := []struct {
		query string
		args  []interface{}
	}{
		{query: `INSERT INTO conversation (id, created_at, last_activity, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, args: []interface{}{rollbackConversationID, activityAt, activityAt, "succeeded", "u1"}},
		{query: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, args: []interface{}{rollbackTurnID, rollbackConversationID, "succeeded"}},
		{query: `INSERT INTO investigation (id, title, created_by, conversation_id) VALUES (?, ?, ?, ?)`, args: []interface{}{rollbackInvestigationID, "retained after rollback", "u1", rollbackConversationID}},
	}
	for _, statement := range rollbackStatements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL maintenance rollback test with %q: %v", statement.query, err)
		}
	}
	if _, err := db.Exec("CREATE TABLE `" + guardTable + "` LIKE conversation"); err != nil {
		t.Fatalf("create MySQL maintenance rollback guard: %v", err)
	}
	if _, err := db.Exec("ALTER TABLE `" + guardTable + "` ADD FOREIGN KEY (`id`) REFERENCES conversation (`id`)"); err != nil {
		t.Fatalf("add MySQL maintenance rollback guard: %v", err)
	}
	if _, err := db.Exec("INSERT INTO `"+guardTable+"` (id) VALUES (?)", rollbackConversationID); err != nil {
		t.Fatalf("seed MySQL maintenance rollback guard: %v", err)
	}

	request.RootID = rollbackConversationID
	request.Mode = ConversationMaintenanceDelete
	_, err = service.MaintainConversationTree(context.Background(), request)
	if err == nil {
		t.Fatal("expected guarded MySQL maintenance delete to fail")
	}
	assertStage1RowCount(t, db, "conversation", "id", rollbackConversationID, 1)
	assertStage1RowCount(t, db, "turn", "id", rollbackTurnID, 1)
	var investigationConversationID sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM investigation WHERE id = ?`, rollbackInvestigationID).Scan(&investigationConversationID); err != nil {
		t.Fatalf("query MySQL investigation after rollback: %v", err)
	}
	if !investigationConversationID.Valid || investigationConversationID.String != rollbackConversationID {
		t.Fatalf("MySQL investigation delete should be rolled back, got %#v", investigationConversationID)
	}
}

func cleanupMySQLDeleteTestRows(t *testing.T, db *sql.DB, ids map[string]string) {
	t.Helper()
	statements := []struct {
		query string
		id    string
	}{
		{query: `DELETE FROM investigation WHERE id = ?`, id: ids["investigation"]},
		{query: `DELETE FROM report_audit_event WHERE event_id = ?`, id: ids["report_audit"]},
		{query: `DELETE FROM report_audit_event WHERE event_id = ?`, id: ids["shared_audit"]},
		{query: `DELETE FROM report_export_artifact WHERE artifact_id = ?`, id: ids["report_artifact"]},
		{query: `DELETE FROM report_export_job WHERE job_id = ?`, id: ids["report_job"]},
		{query: `DELETE FROM conversation_report_context WHERE active_report_run_id = ?`, id: ids["report_run"]},
		{query: `DELETE FROM report_run WHERE report_run_id = ?`, id: ids["report_run"]},
		{query: `DELETE FROM report_shared_artifact WHERE artifact_id = ?`, id: ids["shared_report"]},
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
