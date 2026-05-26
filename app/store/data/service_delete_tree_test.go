package data

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	agpayload "github.com/viant/agently-core/pkg/agently/payload"
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

func seedForStaleActiveConversation(t *testing.T, db *sql.DB) {
	t.Helper()
	old := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
	dbtest.ExecAll(t, db, []dbtest.ParameterizedSQL{
		dbtest.ParameterizedSQL{SQL: `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"conv-stale", old, old, "running", "u1"}},
	})
}
