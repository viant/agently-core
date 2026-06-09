package data

import (
	"context"
	"database/sql"
	"testing"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	_ "modernc.org/sqlite"
)

func TestDataService_ControllerSnapshotCounts(t *testing.T) {
	svc := newSeededService(t, seedForControllerSnapshotCounts)
	ctx := context.Background()

	counter, ok := svc.(interface {
		CountControllerTurns(ctx context.Context, conversationID string, opts ...Option) (int, error)
		CountPendingApprovals(ctx context.Context, conversationID string, opts ...Option) (int, error)
		CountPendingElicitations(ctx context.Context, conversationID string, opts ...Option) (int, error)
	})
	if !ok {
		t.Fatalf("service does not expose controller snapshot count methods")
	}

	cases := []struct {
		name            string
		conversationID  string
		wantController  int
		wantApprovals   int
		wantElicitation int
	}{
		{name: "goal conversation", conversationID: "c-goal", wantController: 2, wantApprovals: 1, wantElicitation: 1},
		{name: "other conversation", conversationID: "c-other", wantController: 1, wantApprovals: 1, wantElicitation: 0},
		{name: "unknown conversation", conversationID: "c-missing", wantController: 0, wantApprovals: 0, wantElicitation: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCtrl, err := counter.CountControllerTurns(ctx, tc.conversationID)
			if err != nil {
				t.Fatalf("CountControllerTurns() error: %v", err)
			}
			if gotCtrl != tc.wantController {
				t.Fatalf("CountControllerTurns() = %d, want %d", gotCtrl, tc.wantController)
			}
			gotApprovals, err := counter.CountPendingApprovals(ctx, tc.conversationID)
			if err != nil {
				t.Fatalf("CountPendingApprovals() error: %v", err)
			}
			if gotApprovals != tc.wantApprovals {
				t.Fatalf("CountPendingApprovals() = %d, want %d", gotApprovals, tc.wantApprovals)
			}
			gotElic, err := counter.CountPendingElicitations(ctx, tc.conversationID)
			if err != nil {
				t.Fatalf("CountPendingElicitations() error: %v", err)
			}
			if gotElic != tc.wantElicitation {
				t.Fatalf("CountPendingElicitations() = %d, want %d", gotElic, tc.wantElicitation)
			}
		})
	}
}

func seedForControllerSnapshotCounts(t *testing.T, db *sql.DB) {
	t.Helper()
	items := []dbtest.ParameterizedSQL{
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-goal", "2026-01-01T09:00:00Z"}},
		{SQL: `INSERT INTO conversation (id, created_at) VALUES (?, ?)`, Params: []interface{}{"c-other", "2026-01-01T09:00:00Z"}},

		// c-goal: two controller-origin turns, one user-origin turn.
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, origin) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-ctrl-1", "c-goal", "2026-01-01T09:01:00Z", "succeeded", "controller"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, origin) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-ctrl-2", "c-goal", "2026-01-01T09:02:00Z", "queued", "controller"}},
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, origin) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-user-1", "c-goal", "2026-01-01T09:03:00Z", "succeeded", "user"}},
		// c-other: a single controller-origin turn.
		{SQL: `INSERT INTO turn (id, conversation_id, created_at, status, origin) VALUES (?, ?, ?, ?, ?)`, Params: []interface{}{"t-ctrl-o", "c-other", "2026-01-01T09:04:00Z", "succeeded", "controller"}},

		// Approvals: one pending + one resolved in c-goal, one pending in c-other.
		{SQL: `INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"a-pending-1", "u1", "c-goal", "sql/query", []byte("{}"), "pending"}},
		{SQL: `INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"a-done-1", "u1", "c-goal", "shell/exec", []byte("{}"), "accepted"}},
		{SQL: `INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status) VALUES (?, ?, ?, ?, ?, ?)`, Params: []interface{}{"a-pending-o", "u1", "c-other", "sql/query", []byte("{}"), "pending"}},

		// Elicitations: one pending + one resolved in c-goal.
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, elicitation_id, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-elic-pending", "c-goal", "t-ctrl-1", "2026-01-01T09:01:10Z", "assistant", "text", "need input", 1, "elic-1", "pending"}},
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, elicitation_id, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-elic-done", "c-goal", "t-user-1", "2026-01-01T09:03:10Z", "assistant", "text", "resolved", 1, "elic-2", "accepted"}},
		// A non-elicitation pending message must not be counted.
		{SQL: `INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, Params: []interface{}{"m-plain", "c-goal", "t-user-1", "2026-01-01T09:03:20Z", "assistant", "text", "plain", 1, "pending"}},
	}
	dbtest.ExecAll(t, db, items)
}
