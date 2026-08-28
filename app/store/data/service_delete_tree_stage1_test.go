package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func TestDeleteConversationTree_BlocksNonTerminalNonEmptyConversation(t *testing.T) {
	svc := newSeededService(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-running", "running", "u1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-running", "conv-running", "running"}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-running")
	if !errors.Is(err, ErrConversationNonTerminal) {
		t.Fatalf("expected ErrConversationNonTerminal, got %v", err)
	}
}

func TestDeleteConversationTree_IgnoresNonTerminalToolStatusWhenConversationIsTerminal(t *testing.T) {
	svc := newSeededService(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-terminal", "succeeded", "u1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-terminal", "conv-terminal", "succeeded"}},
			{SQL: `INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-tool-stale", "conv-terminal", "turn-terminal", "tool", "text", "stale"}},
			{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-tool-stale", "turn-terminal", "op-stale", 1, "test/tool", "mcp", "running"}},
		})
	})

	if err := svc.DeleteConversationTree(deleteTestContext(), "conv-terminal"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}
}

func TestDeleteConversationTree_BlocksInboundLinkFromOutsideGraph(t *testing.T) {
	svc := newSeededService(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-target", "succeeded", "u1"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-source", "succeeded", "u1"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-link", "conv-source", "assistant", "text", "link", "conv-target"}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-target")
	if !errors.Is(err, ErrConversationGraphReferenced) {
		t.Fatalf("expected ErrConversationGraphReferenced, got %v", err)
	}
}

func TestDeleteConversationTree_FollowsParentTurnRelationship(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-parent-turn-root", "succeeded", "u1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-parent", "conv-parent-turn-root", "succeeded"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id, conversation_parent_turn_id) VALUES (?, ?, ?, ?)`, Params: []interface{}{"conv-parent-turn-child", "succeeded", "u1", "turn-parent"}},
		})
	})

	if err := svc.DeleteConversationTree(deleteTestContext(), "conv-parent-turn-root"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-parent-turn-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "conv-parent-turn-child", 0)
}

func TestDeleteConversationTree_HandlesLinkedConversationCycle(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-cycle-a", "succeeded", "u1"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-cycle-b", "succeeded", "u1"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-cycle-a", "conv-cycle-a", "assistant", "text", "to-b", "conv-cycle-b"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-cycle-b", "conv-cycle-b", "assistant", "text", "to-a", "conv-cycle-a"}},
		})
	})

	if err := svc.DeleteConversationTree(deleteTestContext(), "conv-cycle-a"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-cycle-a", 0)
	assertStage1RowCount(t, db, "conversation", "id", "conv-cycle-b", 0)
}

func TestDeleteConversationTree_BlocksLiveRunInDescendant(t *testing.T) {
	now := time.Now().UTC()
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-live-root", "succeeded", "u1"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?)`, Params: []interface{}{"conv-live-child", "succeeded", "u1", "conv-live-root"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-live-child", "conv-live-child", "succeeded"}},
			{SQL: `INSERT INTO run (id, turn_id, conversation_id, conversation_kind, status, lease_until, last_heartbeat_at, heartbeat_interval_sec) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-live-child", "turn-live-child", "conv-live-child", "interactive", "running", now.Add(time.Minute).Format(time.RFC3339), now.Format(time.RFC3339), 5}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-live-root")
	if !errors.Is(err, ErrConversationActive) {
		t.Fatalf("expected ErrConversationActive, got %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-live-root", 1)
	assertStage1RowCount(t, db, "conversation", "id", "conv-live-child", 1)
}

func TestDeleteConversationTree_RequiresOwnershipOfLinkedDescendant(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-owner-root", "succeeded", "u1"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-other-owner-child", "succeeded", "u2"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-owner-link", "conv-owner-root", "assistant", "text", "link", "conv-other-owner-child"}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-owner-root")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-owner-root", 1)
	assertStage1RowCount(t, db, "conversation", "id", "conv-other-owner-child", 1)
}

func TestDeleteConversationTree_BlocksUserScheduleReference(t *testing.T) {
	svc := newSeededService(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-scheduled", "succeeded", "u1"}},
			{SQL: `INSERT INTO schedule (id, name, created_by_user_id, internal, conversation_id, agent_ref, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"schedule-user", "User schedule", "u1", 0, "conv-scheduled", "agent", "adhoc", "UTC"}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-scheduled")
	if !errors.Is(err, ErrConversationScheduleReferenced) {
		t.Fatalf("expected ErrConversationScheduleReferenced, got %v", err)
	}
}

func TestDeleteConversationTree_DeletesCurrentDatabaseDependenciesAndRetainsAuditData(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedStage1CurrentDependencies)

	if err := svc.DeleteConversationTree(deleteTestContext(), "conv-current"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}

	for table, columnAndID := range map[string][2]string{
		"conversation":                {"id", "conv-current"},
		"goal":                        {"id", "goal-current"},
		"schedule":                    {"id", "goal-wakeup-goal-current"},
		"run":                         {"id", "run-goal-wakeup"},
		"tool_execution_claim":        {"claim_key", "claim-current"},
		"report_run":                  {"report_run_id", "report-run-current"},
		"conversation_report_context": {"conversation_id", "conv-current"},
		"report_export_job":           {"job_id", "report-job-current"},
		"report_export_artifact":      {"artifact_id", "report-artifact-current"},
	} {
		assertStage1RowCount(t, db, table, columnAndID[0], columnAndID[1], 0)
	}
	assertStage1RowCount(t, db, "report_audit_event", "event_id", "audit-current", 1)
	assertStage1RowCount(t, db, "report_shared_artifact", "artifact_id", "shared-current", 1)
}

func TestDeleteConversationTree_RetainsPayloadReferencedOutsideGraph(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-payload-root", "succeeded", "u1"}},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-payload-other", "succeeded", "u1"}},
			{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-shared", "attachment", "text/plain", 4, "db", "shared", "none"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-payload-root", "conv-payload-root", "assistant", "text", "root", "payload-shared"}},
			{SQL: `INSERT INTO message (id, conversation_id, role, type, content, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-payload-other", "conv-payload-other", "assistant", "text", "other", "payload-shared"}},
		})
	})

	if err := svc.DeleteConversationTree(deleteTestContext(), "conv-payload-root"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-payload-root", 0)
	assertStage1RowCount(t, db, "conversation", "id", "conv-payload-other", 1)
	assertStage1RowCount(t, db, "call_payload", "id", "payload-shared", 1)
}

func TestDeleteConversationTree_RetainsPayloadForEveryExternalReferenceColumn(t *testing.T) {
	testCases := []struct {
		name   string
		table  string
		column string
	}{
		{name: "message attachment", table: "message", column: "attachment_payload_id"},
		{name: "message elicitation", table: "message", column: "elicitation_payload_id"},
		{name: "model request", table: "model_call", column: "request_payload_id"},
		{name: "model response", table: "model_call", column: "response_payload_id"},
		{name: "model provider request", table: "model_call", column: "provider_request_payload_id"},
		{name: "model provider response", table: "model_call", column: "provider_response_payload_id"},
		{name: "model stream", table: "model_call", column: "stream_payload_id"},
		{name: "tool request", table: "tool_call", column: "request_payload_id"},
		{name: "tool response", table: "tool_call", column: "response_payload_id"},
		{name: "generated file", table: "generated_file", column: "payload_id"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-payload-root", "succeeded", "u1"}},
					{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-payload-other", "succeeded", "u1"}},
					{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-payload-root", "conv-payload-root", "succeeded"}},
					{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-payload-other", "conv-payload-other", "succeeded"}},
					{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, uri, compression) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-shared", "attachment", "text/plain", 4, "db", "shared", "none"}},
					{SQL: `INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-payload-root", "conv-payload-root", "turn-payload-root", "assistant", "text", "root"}},
					{SQL: `INSERT INTO message (id, conversation_id, turn_id, role, type, content) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-payload-other", "conv-payload-other", "turn-payload-other", "assistant", "text", "other"}},
				})
			})

			switch testCase.table {
			case "message":
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: fmt.Sprintf(`UPDATE message SET %s = ? WHERE id = ?`, testCase.column), Params: []interface{}{"payload-shared", "msg-payload-root"}},
					{SQL: fmt.Sprintf(`UPDATE message SET %s = ? WHERE id = ?`, testCase.column), Params: []interface{}{"payload-shared", "msg-payload-other"}},
				})
			case "model_call":
				query := fmt.Sprintf(`INSERT INTO model_call (message_id, turn_id, provider, model, model_kind, status, %s) VALUES (?, ?, ?, ?, ?, ?, ?)`, testCase.column)
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: query, Params: []interface{}{"msg-payload-root", "turn-payload-root", "openai", "gpt", "chat", "completed", "payload-shared"}},
					{SQL: query, Params: []interface{}{"msg-payload-other", "turn-payload-other", "openai", "gpt", "chat", "completed", "payload-shared"}},
				})
			case "tool_call":
				query := fmt.Sprintf(`INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, %s) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, testCase.column)
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: query, Params: []interface{}{"msg-payload-root", "turn-payload-root", "op-root", 1, "test/tool", "mcp", "completed", "payload-shared"}},
					{SQL: query, Params: []interface{}{"msg-payload-other", "turn-payload-other", "op-other", 1, "test/tool", "mcp", "completed", "payload-shared"}},
				})
			case "generated_file":
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: `INSERT INTO generated_file (id, conversation_id, turn_id, message_id, provider, mode, copy_mode, status, payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"file-payload-root", "conv-payload-root", "turn-payload-root", "msg-payload-root", "local", "tool", "eager", "ready", "payload-shared"}},
					{SQL: `INSERT INTO generated_file (id, conversation_id, turn_id, message_id, provider, mode, copy_mode, status, payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"file-payload-other", "conv-payload-other", "turn-payload-other", "msg-payload-other", "local", "tool", "eager", "ready", "payload-shared"}},
				})
			default:
				t.Fatalf("unsupported payload reference table %q", testCase.table)
			}

			if err := svc.DeleteConversationTree(deleteTestContext(), "conv-payload-root"); err != nil {
				t.Fatalf("DeleteConversationTree() error: %v", err)
			}
			assertStage1RowCount(t, db, "conversation", "id", "conv-payload-root", 0)
			assertStage1RowCount(t, db, "conversation", "id", "conv-payload-other", 1)
			assertStage1RowCount(t, db, "call_payload", "id", "payload-shared", 1)
		})
	}
}

func TestDeleteConversationTree_RequiresOwnershipOfReportContext(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		now := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-report-owner", "succeeded", "u1"}},
			{SQL: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"report-run-other-owner", "u2", "conv-report-owner", "test", "completed", now, now, 1, "request-other-owner", now}},
			{SQL: `INSERT INTO conversation_report_context (owner_id, conversation_id, active_report_run_id, revision) VALUES (?, ?, ?, ?)`, Params: []interface{}{"u2", "conv-report-owner", "report-run-other-owner", 1}},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-report-owner")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-report-owner", 1)
}

func TestDeleteConversationTree_IgnoresRunningReportRunWhenConversationIsTerminal(t *testing.T) {
	now := time.Now().UTC()
	testCases := []struct {
		name      string
		updatedAt time.Time
	}{
		{name: "fresh", updatedAt: now},
		{name: "stale", updatedAt: now.Add(-30 * 24 * time.Hour)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			conversationID := "conv-report-running-" + testCase.name
			reportRunID := "report-run-running-" + testCase.name
			svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{conversationID, "succeeded", "u1"}},
					{SQL: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, revision, ui_run_request_id, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{reportRunID, "u1", conversationID, "test", "running", testCase.updatedAt.Format(time.RFC3339), 1, "request-" + testCase.name, testCase.updatedAt.Format(time.RFC3339)}},
				})
			})

			if err := svc.DeleteConversationTree(deleteTestContext(), conversationID); err != nil {
				t.Fatalf("DeleteConversationTree() error: %v", err)
			}
			assertStage1RowCount(t, db, "conversation", "id", conversationID, 0)
			assertStage1RowCount(t, db, "report_run", "report_run_id", reportRunID, 0)
		})
	}
}

func TestDeleteConversationTree_BlocksActiveReportExportJob(t *testing.T) {
	for _, status := range []string{"queued", "running"} {
		t.Run(status, func(t *testing.T) {
			conversationID := "conv-report-export-" + status
			reportRunID := "report-run-export-" + status
			jobID := "report-job-" + status
			now := time.Now().UTC().Format(time.RFC3339)
			svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
				dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
					{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{conversationID, "succeeded", "u1"}},
					{SQL: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{reportRunID, "u1", conversationID, "test", "completed", now, now, 1, "request-export-" + status, now}},
					{SQL: `INSERT INTO report_export_job (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at, report_run_id, report_run_revision, export_request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{jobID, "external://report", "u1", conversationID, "pdf", "draft", status, now, reportRunID, 1, "export-" + status}},
				})
			})

			err := svc.DeleteConversationTree(deleteTestContext(), conversationID)
			if !errors.Is(err, ErrConversationActive) {
				t.Fatalf("expected ErrConversationActive, got %v", err)
			}
			assertStage1RowCount(t, db, "conversation", "id", conversationID, 1)
			assertStage1RowCount(t, db, "report_export_job", "job_id", jobID, 1)
		})
	}
}

func TestDeleteConversationTree_RollsBackEarlierDeletesWhenLaterDeleteFails(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `CREATE TABLE investigation (id TEXT PRIMARY KEY, conversation_id TEXT)`},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-rollback", "succeeded", "u1"}},
			{SQL: `INSERT INTO turn (id, conversation_id, status) VALUES (?, ?, ?)`, Params: []interface{}{"turn-rollback", "conv-rollback", "succeeded"}},
			{SQL: `INSERT INTO investigation (id, conversation_id) VALUES (?, ?)`, Params: []interface{}{"investigation-rollback", "conv-rollback"}},
			{SQL: `CREATE TRIGGER fail_conversation_turn_delete BEFORE DELETE ON turn WHEN OLD.id = 'turn-rollback' BEGIN SELECT RAISE(ABORT, 'forced delete failure'); END`},
		})
	})

	err := svc.DeleteConversationTree(deleteTestContext(), "conv-rollback")
	if err == nil {
		t.Fatal("expected forced delete failure")
	}
	assertStage1RowCount(t, db, "conversation", "id", "conv-rollback", 1)
	var conversationID sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM investigation WHERE id = ?`, "investigation-rollback").Scan(&conversationID); err != nil {
		t.Fatalf("query investigation after rollback: %v", err)
	}
	if !conversationID.Valid || conversationID.String != "conv-rollback" {
		t.Fatalf("investigation detach should be rolled back, got %#v", conversationID)
	}
}

func TestApplyInvestigationDeletePolicy_DeleteModeIsReadyForFutureUse(t *testing.T) {
	_, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `CREATE TABLE investigation (id TEXT PRIMARY KEY, conversation_id TEXT)`},
			{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-investigation-future", "succeeded", "u1"}},
			{SQL: `INSERT INTO investigation (id, conversation_id) VALUES (?, ?)`, Params: []interface{}{"investigation-future", "conv-investigation-future"}},
		})
	})
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin investigation policy transaction: %v", err)
	}
	capabilities, err := deleteSchemaCapabilitiesForDriver("mysql")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create schema capabilities: %v", err)
	}
	graph := &conversationDeleteGraph{
		ConversationIDs: []string{"conv-investigation-future"},
		Capabilities:    capabilities,
	}
	if err := applyInvestigationDeletePolicy(context.Background(), tx, graph, investigationDelete); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply future investigation delete policy: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit investigation policy transaction: %v", err)
	}
	assertStage1RowCount(t, db, "investigation", "id", "investigation-future", 0)
	assertStage1RowCount(t, db, "conversation", "id", "conv-investigation-future", 1)
}

func TestApplyInvestigationDeletePolicy_RetainModeDetachesReference(t *testing.T) {
	_, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
		dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
			{SQL: `CREATE TABLE investigation (id TEXT PRIMARY KEY, conversation_id TEXT)`},
			{SQL: `INSERT INTO investigation (id, conversation_id) VALUES (?, ?)`, Params: []interface{}{"investigation-retained", "conv-investigation-retained"}},
		})
	})
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin investigation policy transaction: %v", err)
	}
	capabilities, err := deleteSchemaCapabilitiesForDriver("mysql")
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("create schema capabilities: %v", err)
	}
	graph := &conversationDeleteGraph{
		ConversationIDs: []string{"conv-investigation-retained"},
		Capabilities:    capabilities,
	}
	if err := applyInvestigationDeletePolicy(context.Background(), tx, graph, investigationRetainAndDetach); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply investigation retain policy: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit investigation policy transaction: %v", err)
	}

	var conversationID sql.NullString
	if err := db.QueryRow(`SELECT conversation_id FROM investigation WHERE id = ?`, "investigation-retained").Scan(&conversationID); err != nil {
		t.Fatalf("query retained investigation: %v", err)
	}
	if conversationID.Valid {
		t.Fatalf("investigation conversation_id should be detached, got %q", conversationID.String)
	}
}

func TestDeleteSchemaCapabilitiesForDriver_UsesStaticContracts(t *testing.T) {
	mysqlCapabilities, err := deleteSchemaCapabilitiesForDriver("mysql")
	if err != nil {
		t.Fatalf("create MySQL schema capabilities: %v", err)
	}
	if !mysqlCapabilities.hasColumn("investigation", "conversation_id") || !mysqlCapabilities.hasColumn("schedule_run", "conversation_id") {
		t.Fatal("MySQL schema contract should include investigation and schedule_run")
	}

	sqliteCapabilities, err := deleteSchemaCapabilitiesForDriver("sqlite")
	if err != nil {
		t.Fatalf("create SQLite schema capabilities: %v", err)
	}
	if !sqliteCapabilities.hasColumn("conversation", "conversation_parent_turn_id") {
		t.Fatal("SQLite schema contract should include current conversation columns")
	}
	if sqliteCapabilities.hasTable("investigation") || sqliteCapabilities.hasTable("schedule_run") {
		t.Fatal("SQLite schema contract should exclude tables absent from the embedded schema")
	}

	if _, err := deleteSchemaCapabilitiesForDriver("postgres"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestCollectConversationTree_RejectsOversizedRootSetBeforeQuery(t *testing.T) {
	ids := make([]string, maxConversationGraph+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("conv-%05d", i)
	}
	_, err := collectConversationTree(context.Background(), nil, ids)
	if !errors.Is(err, ErrConversationGraphTooLarge) {
		t.Fatalf("expected ErrConversationGraphTooLarge, got %v", err)
	}
}

func TestConversationDeleteSchemaManifest_CoversCurrentSQLiteReferences(t *testing.T) {
	_, db := newSeededServiceWithDB(t)
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatalf("list SQLite tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			t.Fatalf("scan SQLite table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close SQLite table rows: %v", err)
	}

	manifest := makeStringSet(conversationDeleteSchemaTables)
	referenceColumns := statusSet(
		"conversation_id", "conversation_parent_id", "conversation_parent_turn_id", "linked_conversation_id",
		"turn_id", "message_id", "parent_message_id", "started_by_message_id", "superseded_by", "checkpoint_message_id",
		"run_id", "resumed_from_run_id", "schedule_id", "schedule_run_id", "goal_id",
		"payload_id", "attachment_payload_id", "elicitation_payload_id", "request_payload_id", "response_payload_id",
		"provider_request_payload_id", "provider_response_payload_id", "stream_payload_id",
		"report_run_id", "active_report_run_id", "job_id", "artifact_id", "source_artifact_id",
	)
	for _, table := range tables {
		columnRows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			t.Fatalf("inspect SQLite table %s: %v", table, err)
		}
		for columnRows.Next() {
			var cid int
			var name, dataType string
			var notNull, primaryKey int
			var defaultValue interface{}
			if err := columnRows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = columnRows.Close()
				t.Fatalf("scan SQLite column for %s: %v", table, err)
			}
			if _, isReference := referenceColumns[normalizeStatus(name)]; !isReference {
				continue
			}
			if _, covered := manifest[normalizeStatus(table)]; !covered {
				_ = columnRows.Close()
				t.Fatalf("table %s has deletion-related column %s but is absent from conversationDeleteSchemaTables", table, name)
			}
		}
		if err := columnRows.Close(); err != nil {
			t.Fatalf("close SQLite columns for %s: %v", table, err)
		}
	}
}

func deleteTestContext() context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
}

func assertStage1RowCount(t *testing.T, db *sql.DB, table, column, id string, want int) {
	t.Helper()
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
	if err := db.QueryRow(query, id).Scan(&count); err != nil {
		t.Fatalf("count %s.%s=%s: %v", table, column, id, err)
	}
	if count != want {
		t.Fatalf("count %s.%s=%s = %d, want %d", table, column, id, count, want)
	}
}

func seedStage1CurrentDependencies(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, status, created_by_user_id) VALUES (?, ?, ?)`, Params: []interface{}{"conv-current", "succeeded", "u1"}},
		{SQL: `INSERT INTO goal (id, conversation_id, objective, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"goal-current", "conv-current", "finish", "complete"}},
		{SQL: `INSERT INTO turn (id, conversation_id, goal_id, status) VALUES (?, ?, ?, ?)`, Params: []interface{}{"turn-current", "conv-current", "goal-current", "succeeded"}},
		{SQL: `INSERT INTO tool_execution_claim (claim_key, rule_id, canonical_tool_name, turn_id, semantic_request_hash, state) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"claim-current", "rule", "test/tool", "turn-current", "hash", "completed"}},
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, internal, conversation_id, goal_id, agent_ref, schedule_type, timezone) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"goal-wakeup-goal-current", "autonomous::goal-wakeup::goal-current", "u1", 1, "conv-current", "goal-current", "agent", "adhoc", "UTC"}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-goal-wakeup", "goal-wakeup-goal-current", "scheduled", "succeeded", now, now, now}},
		{SQL: `INSERT INTO report_run (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"report-run-current", "u1", "conv-current", "test", "completed", now, now, 1, "request-current", now}},
		{SQL: `INSERT INTO conversation_report_context (owner_id, conversation_id, active_report_run_id, revision) VALUES (?, ?, ?, ?)`, Params: []interface{}{"u1", "conv-current", "report-run-current", 1}},
		{SQL: `INSERT INTO report_export_job (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at, report_run_id, report_run_revision, export_request_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"report-job-current", "external://report", "u1", "conv-current", "pdf", "draft", "succeeded", now, "report-run-current", 1, "export-current"}},
		{SQL: `INSERT INTO report_export_artifact (artifact_id, job_id, artifact_ref, owner_id, format, content_type) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"report-artifact-current", "report-job-current", "external://report.pdf", "u1", "pdf", "application/pdf"}},
		{SQL: `INSERT INTO report_audit_event (event_id, event_type, artifact_ref, job_id, artifact_id, actor_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"audit-current", "export", "external://report.pdf", "report-job-current", "report-artifact-current", "u1"}},
		{SQL: `INSERT INTO report_shared_artifact (artifact_id, artifact_ref, owner_id, kind, lifecycle) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"shared-current", "external://shared", "u1", "report", "retained"}},
	})
}
