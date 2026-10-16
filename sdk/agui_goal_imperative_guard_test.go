package sdk

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	aguistore "github.com/viant/agently-core/app/store/agui"
	dexec "github.com/viant/datly/exec"
	"testing"
)

type countedGoalCommand struct {
	delegate *goalTransactionExecutor
	calls    int
}

func (c *countedGoalCommand) Execute(ctx context.Context, invoker dexec.ComponentInvoker, record *aguistore.Run) ([]json.RawMessage, error) {
	c.calls++
	return c.delegate.Execute(ctx, invoker, record)
}
func TestAGUIGoalImperativeCoordinatorRejectsStaleAuthorityBeforeCapability(t *testing.T) {
	enableAtomicGoals(t)
	server, repository, _ := atomicGoalFixture(t)
	backend := &backendClient{goalRepo: repository, goalInvoker: server}
	store := aguistore.New(server)
	ctx := context.Background()
	payload := json.RawMessage(`{"objective":"must not run on stale owner"}`)
	record := atomicGoalRecord(t, store, "goal.create", "guarded-command", payload)
	for _, name := range []string{"revision", "lease-owner", "principal"} {
		input := &aguistore.CommandInput{Principal: record.Principal, ThreadID: record.ThreadID, RunID: record.RunID, ExpectedRevision: record.Revision, LeaseOwner: record.LeaseOwner}
		switch name {
		case "revision":
			input.ExpectedRevision--
		case "lease-owner":
			input.LeaseOwner = "foreign-lease"
		case "principal":
			input.Principal = "foreign-principal"
		}
		capability := &countedGoalCommand{delegate: &goalTransactionExecutor{backend: backend, operation: "goal.create", payload: payload, buffer: &goalNotificationBuffer{}}}
		_, _, err := aguistore.ExecuteCommand(ctx, server, input, capability)
		if name == "principal" {
			require.ErrorIs(t, err, aguistore.ErrNotFound)
		} else {
			require.ErrorIs(t, err, aguistore.ErrConflict)
		}
		require.Zero(t, capability.calls, "stale authority must not execute the domain capability")
		goal, err := backend.GetGoal(ctx, record.ConversationID)
		require.NoError(t, err)
		require.Nil(t, goal)
		unchanged, err := store.GetRun(ctx, record.Principal, record.ThreadID, record.RunID)
		require.NoError(t, err)
		require.Equal(t, record, unchanged)
		events, err := store.Replay(ctx, record.Principal, record.ThreadID, record.RunID, 0, 100)
		require.NoError(t, err)
		require.Empty(t, events)
	}
}
