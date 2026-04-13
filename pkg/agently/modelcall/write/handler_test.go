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

func TestModelCallWrite_SQLite(t *testing.T) {
	type operation struct {
		name  string
		input *ModelCall
		check func(t *testing.T, db *sql.DB)
	}

	type testCase struct {
		name string
		seed func(t *testing.T, db *sql.DB)
		ops  []operation
	}

	cases := []testCase{
		{
			name: "insert model call",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedModelCallGraph(t, db, "c-insert", "t-insert", "m-insert")
			},
			ops: []operation{
				{
					name: "create",
					input: func() *ModelCall {
						mc := &ModelCall{Has: &ModelCallHas{}}
						mc.SetMessageID("m-insert")
						mc.SetTurnID("t-insert")
						mc.SetProvider("openai")
						mc.SetModel("gpt-5")
						mc.SetModelKind("chat")
						mc.SetStatus("thinking")
						mc.SetRunID("run-insert")
						mc.SetIteration(1)
						return mc
					}(),
					check: func(t *testing.T, db *sql.DB) {
						t.Helper()
						var provider, model, modelKind, status string
						var runID sql.NullString
						var iteration sql.NullInt64
						err := db.QueryRow(`SELECT provider, model, model_kind, status, run_id, iteration FROM model_call WHERE message_id = ?`, "m-insert").
							Scan(&provider, &model, &modelKind, &status, &runID, &iteration)
						require.NoError(t, err)
						require.Equal(t, "openai", provider)
						require.Equal(t, "gpt-5", model)
						require.Equal(t, "chat", modelKind)
						require.Equal(t, "thinking", status)
						require.Equal(t, "run-insert", runID.String)
						require.EqualValues(t, 1, iteration.Int64)
					},
				},
			},
		},
		{
			name: "update existing model call via second patch",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				seedModelCallGraph(t, db, "c-update", "t-update", "m-update")
			},
			ops: []operation{
				{
					name: "create",
					input: func() *ModelCall {
						mc := &ModelCall{Has: &ModelCallHas{}}
						mc.SetMessageID("m-update")
						mc.SetTurnID("t-update")
						mc.SetProvider("openai")
						mc.SetModel("gpt-5")
						mc.SetModelKind("chat")
						mc.SetStatus("thinking")
						mc.SetRunID("run-update")
						mc.SetIteration(2)
						return mc
					}(),
				},
				{
					name: "update",
					input: func() *ModelCall {
						mc := &ModelCall{Has: &ModelCallHas{}}
						mc.SetMessageID("m-update")
						mc.SetStatus("completed")
						mc.SetTraceID("resp-123")
						mc.SetCompletionTokens(42)
						return mc
					}(),
					check: func(t *testing.T, db *sql.DB) {
						t.Helper()
						var count int
						require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM model_call WHERE message_id = ?`, "m-update").Scan(&count))
						require.Equal(t, 1, count)

						var status string
						var traceID sql.NullString
						var completionTokens sql.NullInt64
						err := db.QueryRow(`SELECT status, trace_id, completion_tokens FROM model_call WHERE message_id = ?`, "m-update").
							Scan(&status, &traceID, &completionTokens)
						require.NoError(t, err)
						require.Equal(t, "completed", status)
						require.Equal(t, "resp-123", traceID.String)
						require.EqualValues(t, 42, completionTokens.Int64)
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-modelcall-write")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			tc.seed(t, db)

			ctx := context.Background()
			svc, err := newModelCallWriteDatlyService(ctx, dbPath)
			require.NoError(t, err)
			_, err = DefineComponent(ctx, svc)
			require.NoError(t, err)

			for _, op := range tc.ops {
				t.Run(op.name, func(t *testing.T) {
					in := &Input{ModelCalls: []*ModelCall{op.input}}
					out := &Output{}
					_, err := svc.Operate(ctx,
						datly.WithPath(contract.NewPath("PATCH", PathURI)),
						datly.WithInput(in),
						datly.WithOutput(out),
					)
					require.NoError(t, err)
					require.Equal(t, "ok", out.Status.Status, out.Status.Message)
					require.Empty(t, out.Violations)
					require.Len(t, out.Data, 1)
					require.Equal(t, op.input.MessageID, out.Data[0].MessageID)
					if op.check != nil {
						op.check(t, db)
					}
				})
			}
		})
	}
}

func newModelCallWriteDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
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

func seedModelCallGraph(t *testing.T, db *sql.DB, conversationID, turnID, messageID string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO conversation (id, status, created_at) VALUES (?, ?, ?)`, conversationID, "active", "2026-01-01T09:00:00Z")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO turn (id, conversation_id, created_at, status) VALUES (?, ?, ?, ?)`, turnID, conversationID, "2026-01-01T09:01:00Z", "queued")
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO message (id, conversation_id, turn_id, role, type, created_at) VALUES (?, ?, ?, ?, ?, ?)`, messageID, conversationID, turnID, "assistant", "text", "2026-01-01T09:01:00Z")
	require.NoError(t, err)
}
