package sdk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	"github.com/viant/agently-core/app/store/data"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/runtime/streaming"
	agentsvc "github.com/viant/agently-core/service/agent"
	goalsys "github.com/viant/agently-core/service/goal"
	"github.com/viant/agently-core/service/scheduler"
	"github.com/viant/agently-core/workspace"
	"github.com/viant/datly/standalone"
)

type goalEventCollector struct{ events []*streaming.Event }

func (c *goalEventCollector) Publish(_ context.Context, event *streaming.Event) error {
	c.events = append(c.events, event)
	return nil
}
func (c *goalEventCollector) Subscribe(context.Context, streaming.Filter) (streaming.Subscription, error) {
	return nil, fmt.Errorf("not used")
}

func atomicGoalRecord(t *testing.T, store aguistore.Store, op, runID string, payload json.RawMessage) *aguistore.Run {
	t.Helper()
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	raw, _ := json.Marshal(map[string]any{"threadId": "goal-thread", "runId": runID, "messages": []any{}, "forwardedProps": map[string]any{"agently": map[string]any{"version": "1", "operation": op, "requestId": runID, "payload": payload}}})
	run, _, err := store.Admit(context.Background(), aguistore.Admission{Principal: "owner", ThreadID: "goal-thread", RunID: runID, Input: raw})
	require.NoError(t, err)
	run, err = store.Claim(context.Background(), "owner", "goal-thread", runID, run.Revision, "observer", time.Minute)
	require.NoError(t, err)
	return run
}
func enableAtomicGoals(t *testing.T) {
	t.Helper()
	previous := workspace.Root()
	root := t.TempDir()
	workspace.SetRoot(root)
	t.Cleanup(func() { workspace.SetRoot(previous) })
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n"), 0600))
}

func TestAGUIGoalTransactionCommitsAndReplaysWithoutMutation(t *testing.T) {
	enableAtomicGoals(t)
	server, repository, _ := atomicGoalFixture(t)
	backend := &backendClient{goalRepo: repository, goalInvoker: server}
	store := aguistore.New(server)
	ctx := context.Background()
	payload := json.RawMessage(`{"objective":"atomic work","tokenBudget":1000}`)
	record := atomicGoalRecord(t, store, "goal.create", "create", payload)
	next, err := executeAGUIGoalTransaction(ctx, backend, store, record, "goal.create", payload)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, next.Status)
	require.Equal(t, int64(3), next.LastSequence)
	created, err := backend.GetGoal(ctx, "goal-thread")
	require.NoError(t, err)
	require.Equal(t, "atomic work", created.Objective)
	// Replaying the original admitted record must not invoke create a second time.
	replay, err := executeAGUIGoalTransaction(ctx, backend, store, record, "goal.create", payload)
	require.NoError(t, err)
	require.Equal(t, next, replay)
	events, err := store.Replay(ctx, "owner", "goal-thread", "create", 0, 1000)
	require.NoError(t, err)
	require.Len(t, events, 3)
	require.Contains(t, string(events[1].Event), `"name":"agently.goal.result"`)
}

func TestAGUIGoalTransactionLateFailureRollsBackDomainAndJournal(t *testing.T) {
	enableAtomicGoals(t)
	server, repository, connector := atomicGoalFixture(t)
	backend := &backendClient{goalRepo: repository, goalInvoker: server}
	store := aguistore.New(server)
	ctx := context.Background()
	payload := json.RawMessage(`{"objective":"rollback work"}`)
	record := atomicGoalRecord(t, store, "goal.create", "rollback", payload)
	// Install fixture DDL on this exact native connector. The application command
	// itself uses only generated Datly components and the native transaction owner.
	_, err := connector.ExecContext(ctx, `CREATE TRIGGER reject_goal_journal BEFORE INSERT ON call_payload WHEN NEW.kind='agui.event' AND CAST(NEW.inline_body AS TEXT) LIKE '%agently.goal.result%' BEGIN SELECT RAISE(ABORT,'goal-journal-fixture-reject'); END`)
	require.NoError(t, err)
	_, err = executeAGUIGoalTransaction(ctx, backend, store, record, "goal.create", payload)
	require.Error(t, err)
	absent, err := backend.GetGoal(ctx, "goal-thread")
	require.NoError(t, err)
	require.Nil(t, absent)
	unchanged, err := store.GetRun(ctx, "owner", "goal-thread", "rollback")
	require.NoError(t, err)
	require.Equal(t, record.Revision, unchanged.Revision)
	require.Zero(t, unchanged.LastSequence)
	events, err := store.Replay(ctx, "owner", "goal-thread", "rollback", 0, 1000)
	require.NoError(t, err)
	require.Empty(t, events)
}

func atomicGoalFixture(t *testing.T) (*standalone.Server, goalsys.Repository, *sql.DB) {
	t.Helper()
	workspace := t.TempDir()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspace, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`INSERT INTO conversation(id,visibility,created_by_user_id) VALUES('goal-thread','public','owner')`)
	require.NoError(t, err)
	return server, goalsys.NewStore(server), db
}

func TestAGUIGoalTransactionRollsBackScheduleAndBuffersNotifications(t *testing.T) {
	enableAtomicGoals(t)
	root := workspace.Root()
	require.NoError(t, os.WriteFile(filepath.Join(root, "config.yaml"), []byte("features:\n  goals:\n    enabled: true\n  wakeups:\n    enabled: true\n    minWakeDelaySeconds: 1\n    maxWakeDelaySeconds: 3600\n"), 0600))
	server, repository, db := atomicGoalFixture(t)
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "owner"})
	collector := &goalEventCollector{}
	backend := &backendClient{goalRepo: repository, goalInvoker: server, streaming: collector}
	created, err := backend.CreateGoal(ctx, &CreateGoalInput{ConversationID: "goal-thread", Objective: "scheduled atomic work"})
	require.NoError(t, err)
	nativeSchedule, err := scheduler.NewDatlyStore(ctx, server, data.NewService(server))
	require.NoError(t, err)
	schedules := scheduler.New(nativeSchedule, nil)
	backend.SetScheduler(schedules)
	scheduled, err := schedules.ScheduleGoalWakeup(ctx, agentsvc.GoalWakeupRequest{ConversationID: "goal-thread", GoalID: created.ID, UserID: "owner", AgentID: "coder", WakeAt: time.Now().UTC().Add(time.Hour), Preview: "Continue after checkpoint", Payload: "Continue the goal."})
	require.NoError(t, err)
	require.True(t, scheduled)
	store := aguistore.New(server)
	record := atomicGoalRecord(t, store, "goal.pause", "pause", nil)
	_, err = db.Exec(`CREATE TRIGGER reject_pause_journal BEFORE INSERT ON call_payload WHEN NEW.kind='agui.event' AND CAST(NEW.inline_body AS TEXT) LIKE '%agently.goal.result%' BEGIN SELECT RAISE(ABORT,'pause-journal-fixture-reject'); END`)
	require.NoError(t, err)
	_, err = executeAGUIGoalTransaction(ctx, backend, store, record, "goal.pause", nil)
	require.Error(t, err)
	unchanged, err := backend.GetGoal(ctx, "goal-thread")
	require.NoError(t, err)
	require.Equal(t, "active", unchanged.Status)
	require.NotNil(t, unchanged.ControllerSchedule)
	require.Empty(t, collector.events)
	_, err = db.Exec(`DROP TRIGGER reject_pause_journal`)
	require.NoError(t, err)
	next, err := executeAGUIGoalTransaction(ctx, backend, store, record, "goal.pause", nil)
	require.NoError(t, err)
	require.Equal(t, aguistore.StatusFinished, next.Status)
	paused, err := backend.GetGoal(ctx, "goal-thread")
	require.NoError(t, err)
	require.Equal(t, "paused", paused.Status)
	require.Nil(t, paused.ControllerSchedule)
	require.Len(t, collector.events, 1)
	require.Equal(t, streaming.EventTypeGoalUpdated, collector.events[0].Type)
	_, err = executeAGUIGoalTransaction(ctx, backend, store, record, "goal.pause", nil)
	require.NoError(t, err)
	require.Len(t, collector.events, 1, "terminal replay must not publish another tentative goal state")
}
