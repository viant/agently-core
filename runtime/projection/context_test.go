package projection

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWithState(t *testing.T) {
	ctx := WithState(context.Background())
	state, ok := StateFromContext(ctx)
	require.True(t, ok)
	require.NotNil(t, state)

	ctx2 := WithState(ctx)
	state2, ok := StateFromContext(ctx2)
	require.True(t, ok)
	require.Same(t, state, state2)
}

func TestProjectionStateSnapshotAndDedup(t *testing.T) {
	ctx := WithState(context.Background())
	state, ok := StateFromContext(ctx)
	require.True(t, ok)

	state.SetScope("conversation")
	state.HideTurns("T1", "T2", "T1", " ")
	state.HideMessages("M1", "M2", "M1")
	state.SetReason("superseded tool calls")
	state.AddTokensFreed(42)
	state.AddTokensFreed(8)

	got, ok := SnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "conversation", got.Scope)
	require.Equal(t, []string{"T1", "T2"}, got.HiddenTurnIDs)
	require.Equal(t, []string{"M1", "M2"}, got.HiddenMessageIDs)
	require.Equal(t, "superseded tool calls", got.Reason)
	require.Equal(t, 50, got.TokensFreed)

	got.HiddenTurnIDs[0] = "mutated"
	got2, ok := SnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []string{"T1", "T2"}, got2.HiddenTurnIDs)
}

func TestProjectionStateAddReason(t *testing.T) {
	ctx := WithState(context.Background())
	state, ok := StateFromContext(ctx)
	require.True(t, ok)

	state.AddReason("tool call supersession")
	state.AddReason("tool call supersession")
	state.AddReason("relevance projection")

	got, ok := SnapshotFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "tool call supersession; relevance projection", got.Reason)
}

func TestProjectionStateHideTurnsConcurrent(t *testing.T) {
	ctx := WithState(context.Background())
	state, ok := StateFromContext(ctx)
	require.True(t, ok)

	inputs := []string{"T1", "T2", "T3", "T1", "T2", "T4", "T5", "T3"}
	var wg sync.WaitGroup
	for _, id := range inputs {
		wg.Add(1)
		go func(turnID string) {
			defer wg.Done()
			state.HideTurns(turnID)
		}(id)
	}
	wg.Wait()

	got, ok := SnapshotFromContext(ctx)
	require.True(t, ok)
	require.Len(t, got.HiddenTurnIDs, 5)
	require.ElementsMatch(t, []string{"T1", "T2", "T3", "T4", "T5"}, got.HiddenTurnIDs)
}
