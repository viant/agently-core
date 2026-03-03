package write

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/datly/view"
	_ "modernc.org/sqlite"
)

func TestTurnQueueWrite_SQLite(t *testing.T) {
	type testCase struct {
		name         string
		seed         func(t *testing.T, db *sql.DB)
		input        *TurnQueue
		expectStatus string
		expectSeq    int64
	}

	cases := []testCase{
		{
			name: "insert turn queue row",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedConversation(t, db, "c1")
				seedTurnAndMessage(t, db, "c1", "t1", "m1")
			},
			input: func() *TurnQueue {
				q := &TurnQueue{Has: &TurnQueueHas{}}
				q.SetId("q1")
				q.SetConversationId("c1")
				q.SetTurnId("t1")
				q.SetMessageId("m1")
				q.SetQueueSeq(10)
				q.SetStatus("queued")
				return q
			}(),
			expectStatus: "queued",
			expectSeq:    10,
		},
		{
			name: "update existing row status",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedConversation(t, db, "c1")
				seedTurnAndMessage(t, db, "c1", "t2", "m2")
				_, err := db.Exec(`INSERT INTO turn_queue (id, conversation_id, turn_id, message_id, queue_seq, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q2", "c1", "t2", "m2", 20, "queued", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
			},
			input: func() *TurnQueue {
				q := &TurnQueue{Has: &TurnQueueHas{}}
				q.SetId("q2")
				q.SetStatus("canceled")
				q.SetQueueSeq(30)
				return q
			}(),
			expectStatus: "canceled",
			expectSeq:    30,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-turnqueue-write")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			tc.seed(t, db)

			ctx := context.Background()
			svc, err := newWriteDatlyService(ctx, dbPath)
			require.NoError(t, err)
			_, err = DefineComponent(ctx, svc)
			require.NoError(t, err)

			in := &Input{Queues: []*TurnQueue{tc.input}}
			out := &Output{}
			_, err = svc.Operate(ctx,
				datly.WithPath(contract.NewPath("PATCH", PathURI)),
				datly.WithInput(in),
				datly.WithOutput(out),
			)
			require.NoError(t, err)
			require.Empty(t, out.Violations)

			var (
				id, status string
				seq        int64
			)
			err = db.QueryRow(`SELECT id, status, queue_seq FROM turn_queue WHERE id = ?`, tc.input.Id).Scan(&id, &status, &seq)
			require.NoError(t, err)
			require.Equal(t, tc.input.Id, id)
			require.Equal(t, tc.expectStatus, status)
			require.Equal(t, tc.expectSeq, seq)
		})
	}
}

func newWriteDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
	svc, err := datly.New(ctx)
	if err != nil {
		return nil, err
	}
	conn := view.NewConnector("agently", "sqlite", dbPath)
	if err := svc.AddConnectors(ctx, conn); err != nil {
		return nil, err
	}
	return svc, nil
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
