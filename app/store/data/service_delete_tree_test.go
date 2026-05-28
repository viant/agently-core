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
	agpayload "github.com/viant/agently-core/pkg/agently/payload"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestDeleteConversationTree_RemovesTreeArtifactsAndUnsharedPayloads(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteConversationTree(ctx, "conv-root"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}

	for _, id := range []string{"conv-root", "conv-child", "conv-linked"} {
		got, err := svc.GetConversation(context.Background(), id, nil)
		if err != nil {
			t.Fatalf("GetConversation(%s) error: %v", id, err)
		}
		if got != nil {
			t.Fatalf("conversation %s still exists: %#v", id, got)
		}
	}
	other, err := svc.GetConversation(context.Background(), "conv-other", nil)
	if err != nil {
		t.Fatalf("GetConversation(conv-other) error: %v", err)
	}
	if other == nil {
		t.Fatalf("expected unrelated conversation to remain")
	}
	if msg, err := svc.GetMessage(context.Background(), "msg-root", nil); err != nil {
		t.Fatalf("GetMessage(msg-root) error: %v", err)
	} else if msg != nil {
		t.Fatalf("expected root message to be deleted")
	}
	if run, err := svc.GetRun(context.Background(), "run-root", nil); err != nil {
		t.Fatalf("GetRun(run-root) error: %v", err)
	} else if run != nil {
		t.Fatalf("expected run to be deleted")
	}

	payloads, err := svc.ListPayloadRows(context.Background(), &agpayload.PayloadRowsInput{
		Ids: []string{"payload-root", "payload-model", "payload-tool", "payload-generated", "payload-shared"},
		Has: &agpayload.PayloadRowsInputHas{Ids: true},
	})
	if err != nil {
		t.Fatalf("ListPayloadRows() error: %v", err)
	}
	if len(payloads) != 1 || payloads[0].Id != "payload-shared" {
		t.Fatalf("unexpected payloads after delete: %#v", payloads)
	}
}

func TestDeleteConversationTree_RequiresOwner(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})

	err := svc.DeleteConversationTree(ctx, "conv-root")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
	got, getErr := svc.GetConversation(context.Background(), "conv-root", nil)
	if getErr != nil {
		t.Fatalf("GetConversation(conv-root) error: %v", getErr)
	}
	if got == nil {
		t.Fatalf("conversation should remain after denied delete")
	}
}

func TestDeleteConversationTree_ReturnsNotFoundForMissingRoot(t *testing.T) {
	svc := newSeededService(t, seedForConversationTreeDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	err := svc.DeleteConversationTree(ctx, "missing-conversation")
	if !errors.Is(err, ErrConversationNotFound) {
		t.Fatalf("expected ErrConversationNotFound, got %v", err)
	}
}

func TestDeleteConversationTree_BlocksRecentActiveConversation(t *testing.T) {
	svc := newSeededService(t, seedForRecentActiveConversation)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	err := svc.DeleteConversationTree(ctx, "conv-active")
	if !errors.Is(err, ErrConversationActive) {
		t.Fatalf("expected ErrConversationActive, got %v", err)
	}
	got, getErr := svc.GetConversation(context.Background(), "conv-active", nil)
	if getErr != nil {
		t.Fatalf("GetConversation(conv-active) error: %v", getErr)
	}
	if got == nil {
		t.Fatalf("active conversation should remain")
	}
}

func TestDeleteConversationTree_AllowsStaleActiveConversation(t *testing.T) {
	svc := newSeededService(t, seedForStaleActiveConversation)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteConversationTree(ctx, "conv-stale"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}
	got, err := svc.GetConversation(context.Background(), "conv-stale", nil)
	if err != nil {
		t.Fatalf("GetConversation(conv-stale) error: %v", err)
	}
	if got != nil {
		t.Fatalf("stale active conversation should be deleted")
	}
}

func TestDeleteConversationTree_RemovesLegacyScheduleRunRows(t *testing.T) {
	svc, db := newSeededServiceWithDB(t, seedForLegacyScheduleRunConversationDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteConversationTree(ctx, "conv-root"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}

	for _, id := range []string{"sr-by-conversation", "sr-by-conversation-column"} {
		if legacyScheduleRunExists(t, db, id) {
			t.Fatalf("legacy schedule_run %s should be deleted", id)
		}
	}
	if !legacyScheduleRunExists(t, db, "sr-other") {
		t.Fatalf("unrelated legacy schedule_run should remain")
	}
}

func TestDeleteConversationTree_RemovesUnsharedElicitationPayload(t *testing.T) {
	svc := newSeededService(t, seedForElicitationPayloadConversationDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteConversationTree(ctx, "conv-root"); err != nil {
		t.Fatalf("DeleteConversationTree() error: %v", err)
	}

	payloads, err := svc.ListPayloadRows(context.Background(), &agpayload.PayloadRowsInput{
		Ids: []string{"payload-elicit", "payload-elicit-shared"},
		Has: &agpayload.PayloadRowsInputHas{Ids: true},
	})
	if err != nil {
		t.Fatalf("ListPayloadRows() error: %v", err)
	}
	if len(payloads) != 1 || payloads[0].Id != "payload-elicit-shared" {
		t.Fatalf("unexpected elicitation payloads after delete: %#v", payloads)
	}
}

func TestDeleteScheduleCascade_RemovesScheduleConversationsAndRuns(t *testing.T) {
	svc := newSeededService(t, seedForScheduleCascadeDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteScheduleCascade(ctx, "sched-delete"); err != nil {
		t.Fatalf("DeleteScheduleCascade() error: %v", err)
	}
	if err := svc.DeleteScheduleCascade(ctx, "sched-delete"); !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound on second delete, got %v", err)
	}
	for _, id := range []string{"conv-sched-old", "conv-sched-new", "conv-sched-child", "conv-sched-linked"} {
		got, err := svc.GetConversation(context.Background(), id, nil)
		if err != nil {
			t.Fatalf("GetConversation(%s) error: %v", id, err)
		}
		if got != nil {
			t.Fatalf("conversation %s still exists", id)
		}
	}
	for _, id := range []string{"run-sched-old", "run-sched-new", "run-no-conv"} {
		got, err := svc.GetRun(context.Background(), id, nil)
		if err != nil {
			t.Fatalf("GetRun(%s) error: %v", id, err)
		}
		if got != nil {
			t.Fatalf("run %s still exists", id)
		}
	}
	got, err := svc.GetConversation(context.Background(), "conv-unrelated", nil)
	if err != nil {
		t.Fatalf("GetConversation(conv-unrelated) error: %v", err)
	}
	if got == nil {
		t.Fatalf("unrelated conversation should remain")
	}
}

func TestDeleteScheduleCascade_RequiresOwner(t *testing.T) {
	svc := newSeededService(t, seedForScheduleCascadeDelete)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u2"})

	err := svc.DeleteScheduleCascade(ctx, "sched-delete")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

func TestDeleteScheduleCascade_BlocksRecentActiveRunWithoutConversation(t *testing.T) {
	svc := newSeededService(t, seedForScheduleCascadeRecentActiveRun)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	err := svc.DeleteScheduleCascade(ctx, "sched-active")
	if !errors.Is(err, ErrConversationActive) {
		t.Fatalf("expected ErrConversationActive, got %v", err)
	}
	got, getErr := svc.GetRun(context.Background(), "run-active-no-conv", nil)
	if getErr != nil {
		t.Fatalf("GetRun(run-active-no-conv) error: %v", getErr)
	}
	if got == nil {
		t.Fatalf("active run should remain")
	}
}

func TestDeleteScheduleCascade_AllowsStaleActiveRunWithoutConversation(t *testing.T) {
	svc := newSeededService(t, seedForScheduleCascadeStaleActiveRun)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})

	if err := svc.DeleteScheduleCascade(ctx, "sched-stale"); err != nil {
		t.Fatalf("DeleteScheduleCascade() error: %v", err)
	}
	got, err := svc.GetRun(context.Background(), "run-stale-no-conv", nil)
	if err != nil {
		t.Fatalf("GetRun(run-stale-no-conv) error: %v", err)
	}
	if got != nil {
		t.Fatalf("stale active run should be deleted")
	}
}

func TestConversationIDsByDepthDesc_OrdersOldestFirstWithinDepth(t *testing.T) {
	old := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	newer := old.Add(time.Minute)
	got := conversationIDsByDepthDesc(map[string]*conversationTreeRow{
		"parent-new": {ID: "parent-new", Depth: 0, CreatedAt: newer},
		"parent-old": {ID: "parent-old", Depth: 0, CreatedAt: old},
		"child-new":  {ID: "child-new", Depth: 1, CreatedAt: newer},
		"child-old":  {ID: "child-old", Depth: 1, CreatedAt: old},
	})
	want := [][]string{{"child-old", "child-new"}, {"parent-old", "parent-new"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("unexpected delete order: got %v want %v", got, want)
	}
}

func newSeededServiceWithDB(t *testing.T, seeds ...seedFn) (Service, *sql.DB) {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-core-data-service")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	for _, seed := range seeds {
		seed(t, db)
	}

	ctx := context.Background()
	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error: %v", err)
	}
	connector := view.NewConnector("agently", "sqlite", dbPath)
	if err = dao.AddConnectors(ctx, connector); err != nil {
		t.Fatalf("AddConnectors() error: %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents() error: %v", err)
	}
	return NewService(dao), db
}

func legacyScheduleRunExists(t *testing.T, db *sql.DB, id string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schedule_run WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("query schedule_run %s: %v", id, err)
	}
	return count > 0
}

func seedForConversationTreeDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-root", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"conv-child", "2026-01-01T09:02:00Z", "2026-01-01T09:03:00Z", "succeeded", "u1", "conv-root"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-linked", "2026-01-01T09:04:00Z", "2026-01-01T09:05:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-other", "2026-01-01T09:06:00Z", "2026-01-01T09:07:00Z", "succeeded", "u1"}},

		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-root", "model_request", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-model", "model_response", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-tool", "tool_response", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-generated", "tool_response", "text/plain", 2, "inline", "ok", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-shared", "tool_response", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},

		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status, run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"turn-root", "conv-root", "2026-01-01T09:01:00Z", 1, "succeeded", "run-root"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-child", "conv-child", "2026-01-01T09:02:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-linked", "conv-linked", "2026-01-01T09:03:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-other", "conv-other", "2026-01-01T09:04:00Z", 1, "succeeded"}},

		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, linked_conversation_id, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-root", "conv-root", "turn-root", "2026-01-01T09:01:10Z", "assistant", "text", "root", "conv-linked", "payload-root"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-shared", "conv-root", "turn-root", "2026-01-01T09:01:20Z", "assistant", "text", "shared", "payload-shared"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-model", "conv-root", "turn-root", "2026-01-01T09:01:30Z", "assistant", "text", "model"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-tool", "conv-root", "turn-root", "2026-01-01T09:01:40Z", "tool", "text", "tool"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-child", "conv-child", "turn-child", "2026-01-01T09:02:10Z", "assistant", "text", "child"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-linked", "conv-linked", "turn-linked", "2026-01-01T09:03:10Z", "assistant", "text", "linked"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, attachment_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-other", "conv-other", "turn-other", "2026-01-01T09:04:10Z", "assistant", "text", "other", "payload-shared"}},

		{SQL: `INSERT INTO run (id, turn_id, conversation_id, conversation_kind, status, created_at, updated_at, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-root", "turn-root", "conv-root", "interactive", "succeeded", "2026-01-01T09:01:00Z", "2026-01-01T09:01:50Z", "2026-01-01T09:01:05Z", "2026-01-01T09:01:50Z"}},
		{SQL: `INSERT INTO model_call (message_id, turn_id, provider, model, model_kind, status, request_payload_id, response_payload_id, run_id, iteration, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-model", "turn-root", "openai", "gpt", "chat", "completed", "payload-model", "payload-model", "run-root", 1, "2026-01-01T09:01:30Z", "2026-01-01T09:01:31Z"}},
		{SQL: `INSERT INTO tool_call (message_id, turn_id, op_id, attempt, tool_name, tool_kind, status, response_payload_id, run_id, iteration, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-tool", "turn-root", "op-1", 1, "sql/query", "mcp", "completed", "payload-tool", "run-root", 1, "2026-01-01T09:01:40Z", "2026-01-01T09:01:41Z"}},
		{SQL: `INSERT INTO generated_file (id, conversation_id, turn_id, message_id, provider, mode, copy_mode, status, payload_id, filename, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"file-root", "conv-root", "turn-root", "msg-tool", "local", "tool", "eager", "ready", "payload-generated", "out.txt", "2026-01-01T09:01:42Z", "2026-01-01T09:01:42Z"}},
		{SQL: `INSERT INTO tool_approval_queue (id, user_id, conversation_id, turn_id, message_id, tool_name, arguments, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"approval-root", "u1", "conv-root", "turn-root", "msg-tool", "sql/query", []byte("{}"), "approved", "2026-01-01T09:01:00Z", "2026-01-01T09:01:30Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForRecentActiveConversation(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		dbtest.ParameterizedSQL{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-active", now, now, "running", "u1"}},
	})
}

func seedForLegacyScheduleRunConversationDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `CREATE TABLE IF NOT EXISTS schedule_run (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME,
			status TEXT NOT NULL DEFAULT 'pending',
			error_message TEXT,
			lease_owner TEXT,
			lease_until DATETIME,
			precondition_ran_at DATETIME,
			precondition_passed INTEGER,
			precondition_result TEXT,
			conversation_id TEXT,
			conversation_kind TEXT NOT NULL DEFAULT 'scheduled',
			scheduled_for DATETIME,
			started_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY (schedule_id) REFERENCES schedule(id) ON DELETE CASCADE,
			FOREIGN KEY (conversation_id) REFERENCES conversation(id) ON DELETE SET NULL
		)`},
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sched-legacy", "Legacy", "u1", "private", "simple", 1, "adhoc", "UTC", "2026-01-01T08:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, schedule_run_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"conv-root", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "succeeded", "u1", "sr-by-conversation-column"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-other", "2026-01-01T10:00:00Z", "2026-01-01T10:01:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sr-by-conversation", "sched-legacy", "conv-root", "succeeded", "scheduled", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "2026-01-01T09:01:00Z"}},
		{SQL: `INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sr-by-conversation-column", "sched-legacy", nil, "succeeded", "scheduled", "2026-01-01T09:02:00Z", "2026-01-01T09:03:00Z", "2026-01-01T09:03:00Z"}},
		{SQL: `INSERT INTO schedule_run (id, schedule_id, conversation_id, status, conversation_kind, created_at, updated_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sr-other", "sched-legacy", "conv-other", "succeeded", "scheduled", "2026-01-01T10:00:00Z", "2026-01-01T10:01:00Z", "2026-01-01T10:01:00Z"}},
	})
}

func seedForElicitationPayloadConversationDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-root", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-other", "2026-01-01T10:00:00Z", "2026-01-01T10:01:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-elicit", "elicitation_response", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO call_payload (id, kind, mime_type, size_bytes, storage, inline_body, compression, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"payload-elicit-shared", "elicitation_response", "application/json", 2, "inline", "{}", "none", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-root", "conv-root", "2026-01-01T09:01:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-other", "conv-other", "2026-01-01T10:01:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, elicitation_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-elicit", "conv-root", "turn-root", "2026-01-01T09:01:10Z", "user", "elicitation_response", "root", "payload-elicit"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, elicitation_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-elicit-shared-root", "conv-root", "turn-root", "2026-01-01T09:01:20Z", "user", "elicitation_response", "shared-root", "payload-elicit-shared"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, elicitation_payload_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-elicit-shared-other", "conv-other", "turn-other", "2026-01-01T10:01:10Z", "user", "elicitation_response", "shared-other", "payload-elicit-shared"}},
	})
}

func seedForStaleActiveConversation(t *testing.T, db *sql.DB) {
	t.Helper()
	old := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		dbtest.ParameterizedSQL{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-stale", old, old, "running", "u1"}},
	})
}

func seedForScheduleCascadeDelete(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sched-delete", "Delete Me", "u1", "private", "simple", 1, "adhoc", "UTC", "2026-01-01T08:00:00Z"}},

		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"conv-sched-old", "2026-01-01T09:00:00Z", "2026-01-01T09:01:00Z", "succeeded", "u1", "sched-delete"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, schedule_id) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"conv-sched-new", "2026-01-01T10:00:00Z", "2026-01-01T10:01:00Z", "succeeded", "u1", "sched-delete"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, schedule_id, conversation_parent_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"conv-sched-child", "2026-01-01T10:02:00Z", "2026-01-01T10:03:00Z", "succeeded", "u1", "sched-delete", "conv-sched-new"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-sched-linked", "2026-01-01T10:04:00Z", "2026-01-01T10:05:00Z", "succeeded", "u1"}},
		{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-unrelated", "2026-01-01T11:00:00Z", "2026-01-01T11:01:00Z", "succeeded", "u1"}},

		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-sched-old", "conv-sched-old", "2026-01-01T09:01:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, queue_seq, status) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"turn-sched-new", "conv-sched-new", "2026-01-01T10:01:00Z", 1, "succeeded"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, linked_conversation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"msg-sched-new", "conv-sched-new", "turn-sched-new", "2026-01-01T10:01:10Z", "assistant", "text", "linked", "conv-sched-linked"}},

		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, conversation_kind, status, created_at, updated_at, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-sched-old", "turn-sched-old", "sched-delete", "conv-sched-old", "scheduled", "succeeded", "2026-01-01T09:01:00Z", "2026-01-01T09:02:00Z", "2026-01-01T09:01:05Z", "2026-01-01T09:02:00Z"}},
		{SQL: `INSERT INTO run (id, turn_id, schedule_id, conversation_id, conversation_kind, status, created_at, updated_at, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-sched-new", "turn-sched-new", "sched-delete", "conv-sched-new", "scheduled", "succeeded", "2026-01-01T10:01:00Z", "2026-01-01T10:02:00Z", "2026-01-01T10:01:05Z", "2026-01-01T10:02:00Z"}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, updated_at, started_at, completed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-no-conv", "sched-delete", "scheduled", "succeeded", "2026-01-01T12:01:00Z", "2026-01-01T12:02:00Z", "2026-01-01T12:01:05Z", "2026-01-01T12:02:00Z"}},
	}
	dbtest.ExecAll(t, db, items)
}

func seedForScheduleCascadeRecentActiveRun(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sched-active", "Active", "u1", "private", "simple", 1, "adhoc", "UTC", now}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, updated_at, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-active-no-conv", "sched-active", "scheduled", "running", now, now, now}},
	})
}

func seedForScheduleCascadeStaleActiveRun(t *testing.T, db *sql.DB) {
	t.Helper()
	stale := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO schedule (id, name, created_by_user_id, visibility, agent_ref, enabled, schedule_type, timezone, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"sched-stale", "Stale", "u1", "private", "simple", 1, "adhoc", "UTC", stale}},
		{SQL: `INSERT INTO run (id, schedule_id, conversation_kind, status, created_at, updated_at, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"run-stale-no-conv", "sched-stale", "scheduled", "running", stale, stale, stale}},
	})
}
