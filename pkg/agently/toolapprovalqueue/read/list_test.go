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
			name: "orders newest first by created_at then id",
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
			expectIDs: []string{"q2", "q1"},
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

func TestQueueRowsRead_SQLite_Predicates(t *testing.T) {
	seed := func(t *testing.T, db *sql.DB) {
		t.Helper()
		seedUser(t, db, "u1")
		seedUser(t, db, "u2")
		_, err := db.Exec(`INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, "conv-a", "2026-01-01T09:00:00Z", "active", "private")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, "conv-b", "2026-01-01T09:01:00Z", "active", "private")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, "conv-c", "2026-01-01T09:02:00Z", "active", "private")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, "turn-a", "conv-a", "2026-01-01T09:10:00Z", "queued")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, "turn-b", "conv-b", "2026-01-01T09:11:00Z", "queued")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, "turn-c", "conv-c", "2026-01-01T09:12:00Z", "queued")
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "msg-a", "conv-a", "turn-a", "2026-01-01T09:20:00Z", "assistant", "text", "a", 0)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "msg-b", "conv-b", "turn-b", "2026-01-01T09:21:00Z", "assistant", "text", "b", 0)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO message (id, conversation_id, turn_id, created_at, role, type, content, interim) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, "msg-c", "conv-c", "turn-c", "2026-01-01T09:22:00Z", "assistant", "text", "c", 0)
		require.NoError(t, err)
		rows := []struct {
			id             string
			userID         string
			conversationID *string
			turnID         *string
			messageID      *string
			toolName       string
			title          string
			status         string
			createdAt      string
		}{
			{
				id:             "q-alpha",
				userID:         "u1",
				conversationID: stringPtr("conv-a"),
				turnID:         stringPtr("turn-a"),
				messageID:      stringPtr("msg-a"),
				toolName:       "system/exec",
				title:          "Alpha",
				status:         "pending",
				createdAt:      "2026-01-01T10:00:00Z",
			},
			{
				id:             "q-beta",
				userID:         "u1",
				conversationID: stringPtr("conv-b"),
				turnID:         stringPtr("turn-b"),
				messageID:      stringPtr("msg-b"),
				toolName:       "resources/read",
				title:          "Beta",
				status:         "approved",
				createdAt:      "2026-01-01T10:01:00Z",
			},
			{
				id:             "q-gamma",
				userID:         "u2",
				conversationID: stringPtr("conv-c"),
				turnID:         stringPtr("turn-c"),
				messageID:      stringPtr("msg-c"),
				toolName:       "system/os/getEnv",
				title:          "Gamma",
				status:         "rejected",
				createdAt:      "2026-01-01T10:02:00Z",
			},
		}
		for _, row := range rows {
			_, err = db.Exec(
				`INSERT INTO tool_approval_queue (id, user_id, conversation_id, turn_id, message_id, tool_name, title, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				row.id,
				row.userID,
				row.conversationID,
				row.turnID,
				row.messageID,
				row.toolName,
				row.title,
				[]byte(`{"ok":true}`),
				row.status,
				row.createdAt,
			)
			require.NoError(t, err)
		}
	}

	cases := []struct {
		name      string
		input     *QueueRowsInput
		expectIDs []string
	}{
		{
			name: "filters by id",
			input: &QueueRowsInput{
				Id:  "q-beta",
				Has: &QueueRowsInputHas{Id: true},
			},
			expectIDs: []string{"q-beta"},
		},
		{
			name: "filters by user id",
			input: &QueueRowsInput{
				UserId: "u1",
				Has:    &QueueRowsInputHas{UserId: true},
			},
			expectIDs: []string{"q-beta", "q-alpha"},
		},
		{
			name: "filters by conversation id",
			input: &QueueRowsInput{
				ConversationId: "conv-a",
				Has:            &QueueRowsInputHas{ConversationId: true},
			},
			expectIDs: []string{"q-alpha"},
		},
		{
			name: "filters by turn id",
			input: &QueueRowsInput{
				TurnId: "turn-b",
				Has:    &QueueRowsInputHas{TurnId: true},
			},
			expectIDs: []string{"q-beta"},
		},
		{
			name: "filters by message id",
			input: &QueueRowsInput{
				MessageId: "msg-c",
				Has:       &QueueRowsInputHas{MessageId: true},
			},
			expectIDs: []string{"q-gamma"},
		},
		{
			name: "filters by tool name",
			input: &QueueRowsInput{
				ToolName: "resources/read",
				Has:      &QueueRowsInputHas{ToolName: true},
			},
			expectIDs: []string{"q-beta"},
		},
		{
			name: "filters by status",
			input: &QueueRowsInput{
				QueueStatus: "pending",
				Has:         &QueueRowsInputHas{QueueStatus: true},
			},
			expectIDs: []string{"q-alpha"},
		},
		{
			name: "combines user and status predicates",
			input: &QueueRowsInput{
				UserId:      "u1",
				QueueStatus: "approved",
				Has:         &QueueRowsInputHas{UserId: true, QueueStatus: true},
			},
			expectIDs: []string{"q-beta"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-read-predicate")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			seed(t, db)

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

func seedUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}

func stringPtr(value string) *string {
	return &value
}
