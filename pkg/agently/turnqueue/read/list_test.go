package read

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
			svc, err := newReadDatlyService(ctx, dbPath)
			require.NoError(t, err)
			require.NoError(t, DefineQueueRowsComponent(ctx, svc))

			out := &QueueRowsOutput{}
			_, err = svc.Operate(ctx,
				datly.WithPath(contract.NewPath("GET", QueueRowsPathURI)),
				datly.WithInput(tc.input),
				datly.WithOutput(out),
			)
			require.NoError(t, err)
			require.Len(t, out.Data, len(tc.expectIDs))
			for i, id := range tc.expectIDs {
				require.Equal(t, id, out.Data[i].Id)
			}
		})
	}
}

func newReadDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
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
