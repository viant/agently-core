package toolapprovalqueue

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

func TestQueueTotalRead_SQLite(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-count")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	seedCountUser(t, db, "u1")
	seedCountConversation(t, db, "conv-a")
	seedCountConversation(t, db, "conv-b")

	for _, row := range []struct {
		id             string
		conversationID string
		status         string
	}{
		{id: "q1", conversationID: "conv-a", status: "pending"},
		{id: "q2", conversationID: "conv-a", status: "pending"},
		{id: "q3", conversationID: "conv-b", status: "approved"},
	} {
		_, err := db.Exec(
			`INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.id,
			"u1",
			row.conversationID,
			"system/exec",
			[]byte(`{"cmd":"echo ok"}`),
			row.status,
			"2026-01-01T10:00:00Z",
		)
		require.NoError(t, err)
	}

	ctx := context.Background()
	svc, err := newCountDatlyService(ctx, dbPath)
	require.NoError(t, err)
	require.NoError(t, DefineQueueTotalComponent(ctx, svc))

	out := &QueueTotalOutput{}
	_, err = svc.Operate(ctx,
		datly.WithPath(contract.NewPath("GET", QueueTotalPathURI)),
		datly.WithInput(&QueueTotalInput{
			UserId:      "u1",
			QueueStatus: "pending",
			Has:         &QueueTotalInputHas{UserId: true, QueueStatus: true},
		}),
		datly.WithOutput(out),
	)
	require.NoError(t, err)
	require.Len(t, out.Data, 1)
	require.Equal(t, 2, out.Data[0].TotalCount)
}

func newCountDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
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

func seedCountUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}

func seedCountConversation(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, id, "2026-01-01T09:00:00Z", "active", "private")
	require.NoError(t, err)
}
