package goal

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/runtime/usage"
	"github.com/viant/datly/bootstrap/connector"
)

// Exercise the root domain store through the same linked in-process runtime
// used by the application. SQL is restricted to schema and fixture setup;
// assertions read persisted state through the generated goal reader.
func TestStoreLinkedRuntime(t *testing.T) {
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..")
	dsn := filepath.Join(t.TempDir(), "goals.db") + "?_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	schema, err := os.ReadFile(filepath.Join(project, "tools", "schema", "schema.sql"))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(schema))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO conversation(id) VALUES ('c1'), ('c2');
INSERT INTO goal(id,conversation_id,objective,status,status_reason,pause_reason,controller_spec,token_budget,tokens_used,time_used_seconds,autonomous_turns_used,consecutive_no_progress,last_continuation_fingerprint,created_at)
VALUES ('g1','c1','preserve objective','active','previous reason','user_requested','{"continueMode":"idle_only","onTurnFinished":"evaluate","onAsyncCompleted":"evaluate"}',100,17,9,3,2,'previous fingerprint','2026-01-01 00:00:00');`)
	require.NoError(t, err)
	server, err := native.New(ctx, native.Options{
		SourceRoot: project,
		Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dsn, MaxOpenConns: 2}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	store := NewStore(server)
	before, err := store.Current(ctx, "c1")
	require.NoError(t, err)
	require.NotNil(t, before)
	require.True(t, before.Autonomous())
	absent, err := store.Current(ctx, "c2")
	require.NoError(t, err)
	require.Nil(t, absent)

	tests := []struct {
		name   string
		mutate func() error
		change func(*Goal)
	}{
		{"usage including zero", func() error { return store.RecordUsage(ctx, "g1", 0, 0) }, func(g *Goal) { g.TokensUsed = 0; g.TimeUsedSeconds = 0 }},
		{"controller including empty fingerprint", func() error { return store.UpdateControllerState(ctx, "g1", 0, 0, "") }, func(g *Goal) {
			g.AutonomousTurnsUsed = 0
			g.ConsecutiveNoProgress = 0
			g.LastContinuationFingerprint = ""
		}},
		{"transition preserves omitted reason", func() error { return store.Transition(ctx, "g1", StatusBlocked, "") }, func(g *Goal) { g.Status = StatusBlocked }},
		{"transition supplies reason", func() error { return store.Transition(ctx, "g1", StatusActive, "resume") }, func(g *Goal) { g.Status = StatusActive; g.StatusReason = "resume" }},
		{"pause preserves omitted reason", func() error { return store.Pause(ctx, "g1", "") }, func(g *Goal) { g.Status = StatusPaused }},
		{"pause supplies reason", func() error { return store.Pause(ctx, "g1", PauseReasonUserRequested) }, func(g *Goal) { g.Status = StatusPaused; g.PauseReason = PauseReasonUserRequested }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expect := *before
			test.change(&expect)
			require.NoError(t, test.mutate())
			after, err := store.Current(ctx, "c1")
			require.NoError(t, err)
			require.NotNil(t, after)
			require.NotNil(t, after.UpdatedAt)
			require.WithinDuration(t, time.Now(), *after.UpdatedAt, time.Minute)
			expect.UpdatedAt = after.UpdatedAt
			require.Equal(t, &expect, after, "unrelated persisted fields must survive each sparse mutation")
			before = after
		})
	}
	t.Run("controller persists budget transition after usage", func(t *testing.T) {
		require.NoError(t, store.Transition(ctx, "g1", StatusActive, "resume"))
		require.NoError(t, store.RecordUsage(ctx, "g1", 99, 0))
		rt := NewRuntime(store)
		agg := &usage.Aggregator{}
		agg.Add("model", 1, 1, 0, 0)
		deactivated := false
		rt.SetDeactivateHook(func(_ context.Context, conversationID, goalID string) {
			require.Equal(t, "c1", conversationID)
			require.Equal(t, "g1", goalID)
			deactivated = true
		})
		action, updated, err := rt.AfterTurn(ctx, &AfterTurnInput{
			ConversationID: "c1", TurnStatus: "succeeded", Usage: agg,
		})
		require.NoError(t, err)
		require.Equal(t, ActionBudgetLimited, action.Kind)
		require.Equal(t, StatusBudgetLimited, updated.Status)
		require.Equal(t, int64(101), updated.TokensUsed)
		require.True(t, deactivated)
		persisted, err := rt.Current(ctx, "c1")
		require.NoError(t, err)
		require.Equal(t, updated, persisted)
		require.Equal(t, "preserve objective", persisted.Objective)
	})
	t.Run("public goal create sparse clear and delete", func(t *testing.T) {
		objective, status, conversationID := "second objective", "active", "c2"
		require.NoError(t, store.Apply(ctx, Mutation{
			ID:             "g2",
			ConversationID: Field[*string]{Present: true, Value: &conversationID},
			Objective:      Field[*string]{Present: true, Value: &objective},
			Status:         Field[*string]{Present: true, Value: &status},
		}))
		created, err := store.Get(ctx, "c2")
		require.NoError(t, err)
		require.Equal(t, "g2", created.ID)
		require.Equal(t, objective, created.Objective)
		reason := "waiting"
		require.NoError(t, store.Apply(ctx, Mutation{
			ID: "g2", StatusReason: Field[*string]{Present: true, Value: &reason},
		}))
		updated, err := store.Get(ctx, "c2")
		require.NoError(t, err)
		require.Equal(t, &reason, updated.StatusReason)
		require.Equal(t, objective, updated.Objective)
		require.NoError(t, store.Apply(ctx, Mutation{
			ID: "g2", StatusReason: Field[*string]{Present: true},
		}))
		cleared, err := store.Get(ctx, "c2")
		require.NoError(t, err)
		require.Nil(t, cleared.StatusReason)
		require.NoError(t, store.Apply(ctx, Mutation{ID: "g2", Delete: true}))
		absent, err := store.Get(ctx, "c2")
		require.NoError(t, err)
		require.Nil(t, absent)
	})
	t.Run("presence, uniqueness, and required parent", func(t *testing.T) {
		status, objective := "paused", "should-not-apply"
		require.NoError(t, store.Apply(ctx, Mutation{
			ID:        "g1",
			Status:    Field[*string]{Value: &status},
			Objective: Field[*string]{Value: &objective},
		}))
		unchanged, err := store.Get(ctx, "c1")
		require.NoError(t, err)
		require.Equal(t, StatusBudgetLimited, Status(unchanged.Status))
		require.Equal(t, "preserve objective", unchanged.Objective)
		duplicateConversation := "c1"
		active := "active"
		require.Error(t, store.Apply(ctx, Mutation{
			ID: "g-duplicate", ConversationID: Field[*string]{Present: true, Value: &duplicateConversation},
			Objective: Field[*string]{Present: true, Value: &objective},
			Status:    Field[*string]{Present: true, Value: &active},
		}))
		missingConversation := "missing-conversation"
		require.Error(t, store.Apply(ctx, Mutation{
			ID: "g-orphan", ConversationID: Field[*string]{Present: true, Value: &missingConversation},
			Objective: Field[*string]{Present: true, Value: &objective},
			Status:    Field[*string]{Present: true, Value: &active},
		}))
	})
	require.Error(t, store.RecordUsage(ctx, "missing", 1, 1), "a usage patch must not create an incomplete goal")
}
