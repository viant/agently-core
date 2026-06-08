package goal

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

func TestGoalRead_SQLite(t *testing.T) {
	type testCase struct {
		name           string
		conversationID string
		seed           func(t *testing.T, db *sql.DB)
		expectID       string
		expectStatus   string
	}

	now := time.Now().UTC()

	cases := []testCase{
		{
			name:           "no goal returns empty",
			conversationID: "c-none",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				insertConversation(t, db, "c-none")
			},
		},
		{
			name:           "goal by conversation returns the only row",
			conversationID: "c-goal",
			seed: func(t *testing.T, db *sql.DB) {
				t.Helper()
				insertConversation(t, db, "c-goal")
				_, err := db.Exec(
					`INSERT INTO goal (id, conversation_id, objective, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
					"g-current", "c-goal", "current", "active", now.Add(-10*time.Minute), now.Add(-5*time.Minute),
				)
				require.NoError(t, err)
			},
			expectID:     "g-current",
			expectStatus: "active",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "agently-goal-read")
			t.Cleanup(cleanup)
			dbtest.LoadSQLiteSchema(t, db)
			if tc.seed != nil {
				tc.seed(t, db)
			}

			ctx := context.Background()
			dao, err := newDatlyService(ctx, dbPath)
			require.NoError(t, err)
			require.NoError(t, DefineGoalComponent(ctx, dao))

			in := &GoalInput{
				ConversationID: tc.conversationID,
				Has:            &GoalInputHas{ConversationID: true},
			}
			out := &GoalOutput{}
			_, err = dao.Operate(
				ctx,
				datly.WithPath(contract.NewPath("GET", GoalPathURI)),
				datly.WithInput(in),
				datly.WithOutput(out),
			)
			require.NoError(t, err)

			if tc.expectID == "" {
				require.Len(t, out.Data, 0)
				return
			}
			require.Len(t, out.Data, 1)
			require.Equal(t, tc.expectID, out.Data[0].Id)
			require.Equal(t, tc.expectStatus, out.Data[0].Status)
		})
	}
}

func newDatlyService(ctx context.Context, dbPath string) (*datly.Service, error) {
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

func insertConversation(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO conversation (id) VALUES (?)`, id)
	require.NoError(t, err)
}
