package read

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	read "github.com/viant/agently-core/internal/datly/turnqueue/read"
	"github.com/viant/agently-core/internal/store/turnqueue"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly/bootstrap/connector"
	"path/filepath"
	"runtime"

	_ "modernc.org/sqlite"
)

func TestQueueRowsRead_SQLite(t *testing.T) {
	type testCase struct {
		name      string
		seed      func(t *testing.T, db *sql.DB)
		input     *QueueRowsInput
		expectIDs []string
	}

	cases := []testCase{
		{
			name: "filters by conversation and status",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedConversation(t, db, "c1")
				seedTurnAndMessage(t, db, "c1", "t1", "m1")
				seedTurnAndMessage(t, db, "c1", "t2", "m2")
				_, err := db.Exec(`INSERT INTO turn_queue (id, conversation_id, turn_id, message_id, queue_seq, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q1", "c1", "t1", "m1", 1, "queued", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO turn_queue (id, conversation_id, turn_id, message_id, queue_seq, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q2", "c1", "t2", "m2", 2, "canceled", "2026-01-01T10:01:00Z")
				require.NoError(t, err)
			},
			input:     &QueueRowsInput{ConversationId: "c1", QueueStatus: "queued", Has: &QueueRowsInputHas{ConversationId: true, QueueStatus: true}},
			expectIDs: []string{"q1"},
		},
		{
			name: "ordered by queue_seq then id",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedConversation(t, db, "c2")
				seedTurnAndMessage(t, db, "c2", "t1", "m1")
				seedTurnAndMessage(t, db, "c2", "t2", "m2")
				_, err := db.Exec(`INSERT INTO turn_queue (id, conversation_id, turn_id, message_id, queue_seq, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q2", "c2", "t2", "m2", 10, "queued", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO turn_queue (id, conversation_id, turn_id, message_id, queue_seq, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q1", "c2", "t1", "m1", 10, "queued", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
			},
			input:     &QueueRowsInput{ConversationId: "c2", Has: &QueueRowsInputHas{ConversationId: true}},
			expectIDs: []string{"q1", "q2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-turnqueue-read")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			tc.seed(t, db)

			ctx := context.Background()
			_, source, _, _ := runtime.Caller(0)
			server, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(source), "../../../.."), Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dbPath}}})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
			input := &read.QueueRowsInput{Id: tc.input.Id, ConversationId: tc.input.ConversationId, TurnId: tc.input.TurnId, MessageId: tc.input.MessageId, QueueStatus: tc.input.QueueStatus}
			if h := tc.input.Has; h != nil {
				input.Has = &read.QueueRowsInputHas{Id: h.Id, ConversationId: h.ConversationId, TurnId: h.TurnId, MessageId: h.MessageId, QueueStatus: h.QueueStatus}
			}
			rows, err := (&turnqueue.Store{Invoker: server}).List(ctx, input)

			require.NoError(t, err)
			require.Len(t, rows, len(tc.expectIDs))
			for i, id := range tc.expectIDs {
				require.Equal(t, id, rows[i].Id)
			}
		})
	}
}

func seedConversation(t *testing.T, db *sql.DB, conversationID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO conversation (id, status, created_at) VALUES (?, ?, ?)`, conversationID, "active", "2026-01-01T09:00:00Z")
	require.NoError(t, err)
}

func seedTurnAndMessage(t *testing.T, db *sql.DB, conversationID, turnID, messageID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, turnID, conversationID, "2026-01-01T09:01:00Z", "queued")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO message (id, conversation_id, turn_id, role, type, created_at) VALUES (?, ?, ?, ?, ?, ?)`, messageID, conversationID, turnID, "user", "task", "2026-01-01T09:01:00Z")
	require.NoError(t, err)
}
