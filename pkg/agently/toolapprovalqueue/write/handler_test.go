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

func TestToolApprovalQueueWrite_SQLite(t *testing.T) {
	type testCase struct {
		name          string
		seed          func(t *testing.T, db *sql.DB)
		input         *ToolApprovalQueue
		expectStatus  string
		expectTitle   string
		expectTool    string
		expectUserID  string
		expectPresent bool
	}

	cases := []testCase{
		{
			name: "insert queue row",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedQueueUser(t, db, "u1")
			},
			input: func() *ToolApprovalQueue {
				q := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
				q.SetId("q-insert")
				q.SetUserId("u1")
				q.SetToolName("system/exec")
				q.SetTitle("Run report")
				q.SetArguments([]byte(`{"cmd":"echo ok"}`))
				q.SetStatus("pending")
				return q
			}(),
			expectStatus:  "pending",
			expectTitle:   "Run report",
			expectTool:    "system/exec",
			expectUserID:  "u1",
			expectPresent: true,
		},
		{
			name: "insert second queue row",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedQueueUser(t, db, "u1")
			},
			input: func() *ToolApprovalQueue {
				q := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
				q.SetId("q-insert-2")
				q.SetUserId("u1")
				q.SetToolName("resources/read")
				q.SetTitle("Load docs")
				q.SetArguments([]byte(`{"path":"workspace://docs"}`))
				q.SetStatus("pending")
				return q
			}(),
			expectStatus:  "pending",
			expectTitle:   "Load docs",
			expectTool:    "resources/read",
			expectUserID:  "u1",
			expectPresent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-write")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			require.NotNil(t, tc.seed)
			tc.seed(t, db)

			ctx := context.Background()
			svc, err := newWriteDatlyService(ctx, dbPath)
			require.NoError(t, err)
			_, err = DefineComponent(ctx, svc)
			require.NoError(t, err)

			in := &Input{Queues: []*ToolApprovalQueue{tc.input}}
			out := &Output{}
			_, err = svc.Operate(ctx,
				datly.WithPath(contract.NewPath("PATCH", PathURI)),
				datly.WithInput(in),
				datly.WithOutput(out),
			)
			require.NoError(t, err)
			require.Empty(t, out.Violations)

			var (
				id, userID, toolName, status string
				title                        sql.NullString
			)
			err = db.QueryRow(`SELECT id, user_id, tool_name, title, status FROM tool_approval_queue WHERE id = ?`, tc.input.Id).
				Scan(&id, &userID, &toolName, &title, &status)
			if tc.expectPresent {
				require.NoError(t, err)
				require.Equal(t, tc.input.Id, id)
				require.Equal(t, tc.expectUserID, userID)
				require.Equal(t, tc.expectTool, toolName)
				require.Equal(t, tc.expectStatus, status)
				require.Equal(t, tc.expectTitle, title.String)
			} else {
				require.Error(t, err)
			}
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

func seedQueueUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}
