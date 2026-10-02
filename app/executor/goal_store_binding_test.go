package executor_test

import (
	"context"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/executor"
	"github.com/viant/agently-core/app/store/data"
	goals "github.com/viant/agently-core/service/goal"
	"testing"
)

type retainedGoalRepository struct{ goals.Repository }

func TestBuilderRetainsInjectedGoalStoreAndBorrowedNativeRuntime(t *testing.T) {
	isolateActiveReportRunTestState(t)
	ctx := context.Background()
	server, err := data.NewRuntimeInMemory(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	injected := &retainedGoalRepository{goals.NewStore(server)}
	rt, err := executor.NewBuilder().WithAgentFinder(stubAgentFinder{}).WithModelFinder(stubModelFinder{}).WithNativeRuntime(server).WithGoalStore(injected).Build(ctx)
	require.NoError(t, err)
	require.Same(t, server, rt.Native)
	require.Same(t, injected, rt.GoalStore)
	require.NoError(t, rt.Close(ctx))
	// Runtime borrows both capabilities. The caller's native store remains usable.
	record, err := injected.Get(ctx, "missing-conversation")
	require.NoError(t, err)
	require.Nil(t, record)
}
