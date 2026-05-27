package toolapprovalqueue

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

func TestOutcomeRowsRead_SQLite(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-toolapproval-outcome")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	seedOutcomeUser(t, db, "u1")
	seedOutcomeConversation(t, db, "conv-a")
	seedOutcomeConversation(t, db, "conv-b")

	approvedAt := mustParseOutcomeTime(t, "2026-01-01T10:01:00Z")
	executedAt := mustParseOutcomeTime(t, "2026-01-01T10:02:00Z")
	_, err := db.Exec(
		`INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status, approved_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"q-approved",
		"u1",
		"conv-a",
		"system/exec",
		[]byte(`{"cmd":"echo approved"}`),
		"approved",
		approvedAt,
		approvedAt,
		approvedAt,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status, executed_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"q-executed",
		"u1",
		"conv-b",
		"system/os/getEnv",
		[]byte(`{"names":["HOME"]}`),
		"executed",
		executedAt,
		executedAt,
		executedAt,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO tool_approval_queue (id, user_id, conversation_id, tool_name, arguments, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"q-pending",
		"u1",
		"conv-a",
		"resources/read",
		[]byte(`{"uri":"workspace://docs"}`),
		"pending",
		approvedAt,
		approvedAt,
	)
	require.NoError(t, err)

	ctx := context.Background()
	svc, err := newOutcomeDatlyService(ctx, dbPath)
	require.NoError(t, err)
	require.NoError(t, DefineOutcomeRowsComponent(ctx, svc))

	in := &OutcomeRowsInput{
		UserId: "u1",
		Since:  mustParseOutcomeTime(t, "2026-01-01T10:00:30Z"),
		Until:  mustParseOutcomeTime(t, "2026-01-01T10:02:30Z"),
		Has:    &OutcomeRowsInputHas{UserId: true, Since: true, Until: true},
	}
	out := &OutcomeRowsOutput{}
	_, err = svc.Operate(ctx,
		datly.WithPath(contract.NewPath("GET", OutcomeRowsPathURI)),
		datly.WithInput(in),
		datly.WithOutput(out),
	)
	require.NoError(t, err)
	require.Len(t, out.Data, 2)
	require.Equal(t, "q-approved", out.Data[0].Id)
	require.Equal(t, "q-executed", out.Data[1].Id)
}

func newOutcomeDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
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

func seedOutcomeUser(t *testing.T, db *sql.DB, userID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, username) VALUES (?, ?)`, userID, userID)
	require.NoError(t, err)
}

func seedOutcomeConversation(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO conversation (id, created_at, status, visibility) VALUES (?, ?, ?, ?)`, id, "2026-01-01T09:00:00Z", "active", "private")
	require.NoError(t, err)
}

func mustParseOutcomeTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed.UTC()
}
