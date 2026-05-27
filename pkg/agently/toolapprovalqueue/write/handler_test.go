package write

import (
	"context"
	"database/sql"
	"testing"
	"time"

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

func TestToolApprovalQueueWrite_SQLite_UpdateSelectiveAndNullableFields(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-write-update")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	seedQueueUser(t, db, "u1")

	createdAt := mustParseUTC(t, "2026-05-26T12:00:00Z")
	expiresAt := mustParseUTC(t, "2026-05-26T12:05:00Z")
	approvedAt := mustParseUTC(t, "2026-05-26T12:01:00Z")
	decision := "approve"
	errMsg := "old warning"
	title := "Original title"
	approvedBy := "reviewer-1"

	_, err := db.Exec(
		`INSERT INTO tool_approval_queue (id, user_id, tool_name, title, arguments, status, decision, expires_at, approved_by_user_id, approved_at, error_message, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"q-update-1",
		"u1",
		"system/exec",
		title,
		[]byte(`{"cmd":"echo old"}`),
		"pending",
		decision,
		expiresAt,
		approvedBy,
		approvedAt,
		errMsg,
		createdAt,
		createdAt,
	)
	require.NoError(t, err)

	ctx := context.Background()
	svc, err := newWriteDatlyService(ctx, dbPath)
	require.NoError(t, err)
	_, err = DefineComponent(ctx, svc)
	require.NoError(t, err)

	t.Run("updates only marked fields and preserves others", func(t *testing.T) {
		updatedAt := mustParseUTC(t, "2026-05-26T12:02:00Z")
		patch := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
		patch.SetId("q-update-1")
		patch.SetUserId("u1")
		patch.SetToolName("system/exec")
		patch.SetArguments([]byte(`{"cmd":"echo old"}`))
		patch.SetStatus("approved")
		patch.SetUpdatedAt(updatedAt)

		out := &Output{}
		_, err = svc.Operate(ctx,
			datly.WithPath(contract.NewPath("PATCH", PathURI)),
			datly.WithInput(&Input{Queues: []*ToolApprovalQueue{patch}}),
			datly.WithOutput(out),
		)
		require.NoError(t, err)
		require.Empty(t, out.Violations)

		var (
			status          string
			gotTitle        sql.NullString
			gotDecision     sql.NullString
			gotExpiresAt    sql.NullString
			gotApprovedBy   sql.NullString
			gotApprovedAt   sql.NullString
			gotErrorMessage sql.NullString
		)
		require.NoError(t, db.QueryRow(
			`SELECT status, title, decision, expires_at, approved_by_user_id, approved_at, error_message FROM tool_approval_queue WHERE id = ?`,
			"q-update-1",
		).Scan(&status, &gotTitle, &gotDecision, &gotExpiresAt, &gotApprovedBy, &gotApprovedAt, &gotErrorMessage))
		require.Equal(t, "approved", status)
		require.Equal(t, title, gotTitle.String)
		require.Equal(t, decision, gotDecision.String)
		require.True(t, gotExpiresAt.Valid)
		require.Equal(t, approvedBy, gotApprovedBy.String)
		require.True(t, gotApprovedAt.Valid)
		require.Equal(t, errMsg, gotErrorMessage.String)
	})

	t.Run("clears nullable fields when marked present with nil", func(t *testing.T) {
		updatedAt := mustParseUTC(t, "2026-05-26T12:03:00Z")
		patch := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
		patch.SetId("q-update-1")
		patch.SetUserId("u1")
		patch.SetToolName("system/exec")
		patch.SetArguments([]byte(`{"cmd":"echo old"}`))
		patch.Status = "approved"
		patch.Has.Status = true
		patch.Decision = nil
		patch.Has.Decision = true
		patch.ExpiresAt = nil
		patch.Has.ExpiresAt = true
		patch.ApprovedByUserId = nil
		patch.Has.ApprovedByUserId = true
		patch.ApprovedAt = nil
		patch.Has.ApprovedAt = true
		patch.ErrorMessage = nil
		patch.Has.ErrorMessage = true
		patch.SetUpdatedAt(updatedAt)

		out := &Output{}
		_, err = svc.Operate(ctx,
			datly.WithPath(contract.NewPath("PATCH", PathURI)),
			datly.WithInput(&Input{Queues: []*ToolApprovalQueue{patch}}),
			datly.WithOutput(out),
		)
		require.NoError(t, err)
		require.Empty(t, out.Violations)

		var (
			gotDecision     sql.NullString
			gotExpiresAt    sql.NullString
			gotApprovedBy   sql.NullString
			gotApprovedAt   sql.NullString
			gotErrorMessage sql.NullString
		)
		require.NoError(t, db.QueryRow(
			`SELECT decision, expires_at, approved_by_user_id, approved_at, error_message FROM tool_approval_queue WHERE id = ?`,
			"q-update-1",
		).Scan(&gotDecision, &gotExpiresAt, &gotApprovedBy, &gotApprovedAt, &gotErrorMessage))
		require.False(t, gotDecision.Valid)
		require.False(t, gotExpiresAt.Valid)
		require.False(t, gotApprovedBy.Valid)
		require.False(t, gotApprovedAt.Valid)
		require.False(t, gotErrorMessage.Valid)
	})
}

// TestToolApprovalQueueWrite_PersistsExpiresAtAndTimedOutAt asserts that
// the canonical persistent contract — expires_at and timed_out_at —
// round-trips through the Datly write path. These columns are how the
// backend timeout producer records the truth of "when this approval
// was scheduled to expire" and "when the producer transitioned it",
// so the read path must observe what the write path persisted.
func TestToolApprovalQueueWrite_PersistsExpiresAtAndTimedOutAt(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-timeout")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	seedQueueUser(t, db, "u1")

	ctx := context.Background()
	svc, err := newWriteDatlyService(ctx, dbPath)
	require.NoError(t, err)
	_, err = DefineComponent(ctx, svc)
	require.NoError(t, err)

	expires := mustParseUTC(t, "2026-05-26T12:00:00Z")
	row := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
	row.SetId("q-timeout-1")
	row.SetUserId("u1")
	row.SetToolName("system/exec")
	row.SetArguments([]byte(`{"cmd":"echo ok"}`))
	row.SetStatus("pending")
	row.SetExpiresAt(expires)

	in := &Input{Queues: []*ToolApprovalQueue{row}}
	out := &Output{}
	_, err = svc.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", PathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	require.NoError(t, err)
	require.Empty(t, out.Violations)

	var expiresAtCol sql.NullString
	require.NoError(t, db.QueryRow(`SELECT expires_at FROM tool_approval_queue WHERE id = ?`, row.Id).Scan(&expiresAtCol))
	require.True(t, expiresAtCol.Valid)

	// Producer-style transition: status, decision, timed_out_at, error_message.
	// The producer reuses identity fields (UserId, ToolName, Arguments) from
	// the source row so the canonical write path remains a pure patch that
	// passes existing required-field validation.
	timedOut := mustParseUTC(t, "2026-05-26T12:00:30Z")
	patch := &ToolApprovalQueue{Has: &ToolApprovalQueueHas{}}
	patch.SetId(row.Id)
	patch.SetUserId(row.UserId)
	patch.SetToolName(row.ToolName)
	patch.SetArguments(row.Arguments)
	patch.SetStatus("timed_out")
	patch.SetDecision("timeout")
	patch.SetTimedOutAt(timedOut)
	patch.SetErrorMessage("approval request timed out")
	patch.SetUpdatedAt(timedOut)

	patchOut := &Output{}
	_, err = svc.Operate(ctx,
		datly.WithPath(contract.NewPath("PATCH", PathURI)),
		datly.WithInput(&Input{Queues: []*ToolApprovalQueue{patch}}),
		datly.WithOutput(patchOut),
	)
	require.NoError(t, err)
	require.Empty(t, patchOut.Violations)

	var (
		status       string
		decision     sql.NullString
		timedOutCol  sql.NullString
		errorMessage sql.NullString
	)
	require.NoError(t, db.QueryRow(
		`SELECT status, decision, timed_out_at, error_message FROM tool_approval_queue WHERE id = ?`,
		row.Id,
	).Scan(&status, &decision, &timedOutCol, &errorMessage))
	require.Equal(t, "timed_out", status)
	require.True(t, decision.Valid)
	require.Equal(t, "timeout", decision.String)
	require.True(t, timedOutCol.Valid)
	require.True(t, errorMessage.Valid)
	require.Equal(t, "approval request timed out", errorMessage.String)
}

func mustParseUTC(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed.UTC()
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
