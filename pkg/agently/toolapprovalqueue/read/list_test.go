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
			name: "lists rows with title and status",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedUser(t, db, "u1")
				_, err := db.Exec(`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q1", "u1", "system/exec", "Run command", []byte(`{"cmd":"echo ok"}`), "pending", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q2", "u1", "system/exec", "Reject me", []byte(`{"cmd":"echo no"}`), "rejected", "2026-01-01T10:01:00Z")
				require.NoError(t, err)
			},
			input: &QueueRowsInput{
				UserId:      "u1",
				QueueStatus: "pending",
				Has:         &QueueRowsInputHas{UserId: true, QueueStatus: true},
			},
			expectIDs: []string{"q1"},
		},
		{
			name: "orders by created_at then id",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedUser(t, db, "u1")
				_, err := db.Exec(`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q2", "u1", "system/exec", "Second", []byte(`{"cmd":"echo 2"}`), "pending", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
				_, err = db.Exec(`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
					"q1", "u1", "system/exec", "FirstByID", []byte(`{"cmd":"echo 1"}`), "pending", "2026-01-01T10:00:00Z")
				require.NoError(t, err)
			},
			input: &QueueRowsInput{
				UserId: "u1",
				Has:    &QueueRowsInputHas{UserId: true},
			},
			expectIDs: []string{"q1", "q2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-read")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			require.NotNil(t, tc.seed)
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
			if len(out.Data) > 0 {
				require.NotNil(t, out.Data[0].Title)
				require.NotEmpty(t, out.Data[0].Status)
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

func seedUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}
