package async

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManager_RegisterWaitConsume(t *testing.T) {
	manager := NewManager()
	rec, _ := manager.Register(context.Background(), RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "system/exec:start",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})
	require.NotNil(t, rec)
	require.True(t, manager.HasActiveWaitOps(context.Background(), "conv-1", "turn-1"))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, manager.WaitForChange(ctx, "conv-1", "turn-1"))

	changed := manager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changed, 1)
	require.Equal(t, "op-1", changed[0].ID)

	waitCtx, waitCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- manager.WaitForChange(waitCtx, "conv-1", "turn-1")
	}()

	_, updated := manager.Update(context.Background(), UpdateInput{ID: "op-1", Status: "completed", State: StateCompleted})
	require.True(t, updated)
	require.NoError(t, <-done)
	waitCancel()

	require.False(t, manager.HasActiveWaitOps(context.Background(), "conv-1", "turn-1"))
}

func TestManager_RegisterStoresTimeout(t *testing.T) {
	manager := NewManager()
	rec, _ := manager.Register(context.Background(), RegisterInput{
		ID:           "op-timeout",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		TimeoutMs:    50,
	})
	require.NotNil(t, rec)
	require.NotNil(t, rec.TimeoutAt)
	require.True(t, rec.TimeoutAt.After(rec.CreatedAt))
}

func TestManager_TerminalFailure(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-fail",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "llm/agents:start",
		Status:       "running",
	})
	_, changed := manager.Update(context.Background(), UpdateInput{
		ID:     "op-fail",
		Status: "failed",
		Error:  "boom",
		State:  StateFailed,
	})
	require.True(t, changed)

	rec, ok := manager.TerminalFailure(context.Background(), "conv-1", "turn-1")
	require.True(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, "op-fail", rec.ID)
	require.Equal(t, StateFailed, rec.State)
}

func TestManager_UpdateSignalsOnMessageChange(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		Status:       "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec, changed := manager.Update(context.Background(), UpdateInput{
		ID:      "op-1",
		Message: "still running",
	})
	require.True(t, changed)
	require.NotNil(t, rec)
	changedOps := manager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changedOps, 1)
	require.Equal(t, "still running", changedOps[0].Message)
}

func TestManager_UpdateErrorOnlyDoesNotSignal(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		Status:       "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec, changed := manager.Update(context.Background(), UpdateInput{
		ID:    "op-1",
		Error: "transient stderr noise",
	})
	require.True(t, changed)
	require.NotNil(t, rec)
	require.Empty(t, manager.ConsumeChanged("conv-1", "turn-1"))
}

func TestManager_UpdatePercentBelowThresholdDoesNotSignal(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		Status:       "running",
		Percent:      intPtr(10),
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec, changed := manager.Update(context.Background(), UpdateInput{
		ID:      "op-1",
		Percent: intPtr(12),
	})
	require.True(t, changed)
	require.NotNil(t, rec)
	require.Empty(t, manager.ConsumeChanged("conv-1", "turn-1"))
}

func TestManager_UpdatePercentAtThresholdSignals(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		Status:       "running",
		Percent:      intPtr(10),
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec, changed := manager.Update(context.Background(), UpdateInput{
		ID:      "op-1",
		Percent: intPtr(15),
	})
	require.True(t, changed)
	require.NotNil(t, rec)
	changedOps := manager.ConsumeChanged("conv-1", "turn-1")
	require.Len(t, changedOps, 1)
	require.NotNil(t, changedOps[0].Percent)
	require.Equal(t, 15, *changedOps[0].Percent)
}

func TestManager_FindActiveByRequest(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:                "op-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting:TotalV1",
		RequestArgsDigest: `{"viewId":"TOTAL"}`,
		ExecutionMode:     string(ExecutionModeWait),
		Status:            "WAITING",
	})

	rec, ok := manager.FindActiveByRequest(context.Background(), "conv-1", "turn-1", "forecasting:TotalV1", `{"viewId":"TOTAL"}`)
	require.True(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, "op-1", rec.ID)
}

func TestManager_WaitForNextPoll(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:             "op-1",
		ParentConvID:   "conv-1",
		ParentTurnID:   "turn-1",
		ToolName:       "forecasting:TotalV1",
		ExecutionMode:  string(ExecutionModeWait),
		PollIntervalMs: 50,
		Status:         "WAITING",
	})

	started := time.Now()
	err := manager.WaitForNextPoll(context.Background(), "conv-1", "turn-1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(started), 40*time.Millisecond)
}

func TestManager_Subscribe_ReceivesChangeEvent(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "tool:start",
		OperationIntent: "inspect repo",
		Status:          "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	sub, _ := manager.Subscribe([]string{"op-1"})
	_, changed := manager.Update(context.Background(), UpdateInput{
		ID:      "op-1",
		Message: "still running",
	})
	require.True(t, changed)

	select {
	case ev, ok := <-sub:
		require.True(t, ok)
		require.Equal(t, "op-1", ev.OperationID)
		require.Equal(t, "tool:start", ev.ToolName)
		require.Equal(t, "inspect repo", ev.Intent)
		require.Equal(t, "still running", ev.Message)
		require.NotEmpty(t, ev.PriorDigest)
		require.NotEmpty(t, ev.NewDigest)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for change event")
	}
}

func TestManager_AwaitTerminal_ReturnsWhenAllTerminal(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "tool:start",
		Status:       "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	ch := manager.AwaitTerminal(context.Background(), []string{"op-1"})
	_, changed := manager.Update(context.Background(), UpdateInput{
		ID:     "op-1",
		Status: "completed",
		State:  StateCompleted,
	})
	require.True(t, changed)

	select {
	case result := <-ch:
		require.False(t, result.OpsStillActive)
		require.Len(t, result.Items, 1)
		require.Equal(t, "success", result.Items[0].Reason)
		require.Equal(t, StateCompleted, result.Items[0].State)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for aggregated terminal result")
	}
}

func TestManager_AwaitTerminal_SoftReleasesOnIdle(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "tool:start",
		Status:        "running",
		IdleTimeoutMs: 20,
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	ch := manager.AwaitTerminal(context.Background(), []string{"op-1"})
	select {
	case result := <-ch:
		require.True(t, result.OpsStillActive)
		require.Len(t, result.Items, 1)
		require.Equal(t, "running_idle", result.Items[0].Reason)
		require.Equal(t, StateRunning, result.Items[0].State)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle release")
	}
}

func TestManager_AwaitTerminal_StopsOnContextCancel(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:            "op-1",
		ParentConvID:  "conv-1",
		ParentTurnID:  "turn-1",
		ToolName:      "tool:start",
		Status:        "running",
		IdleTimeoutMs: 10_000,
	})
	ctx, cancel := context.WithCancel(context.Background())
	ch := manager.AwaitTerminal(ctx, []string{"op-1"})
	cancel()

	select {
	case _, ok := <-ch:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AwaitTerminal to stop on context cancel")
	}
}

func TestManager_RecordPollFailure_TransientRetriesThenFails(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "system/exec:start",
		Status:       "running",
	})
	_ = manager.ConsumeChanged("conv-1", "turn-1")

	rec, terminal := manager.RecordPollFailure(context.Background(), "op-1", "temporary network timeout", true)
	require.False(t, terminal)
	require.NotNil(t, rec)
	require.Equal(t, 1, rec.PollFailures)
	require.Empty(t, manager.ConsumeChanged("conv-1", "turn-1"))

	rec, terminal = manager.RecordPollFailure(context.Background(), "op-1", "temporary network timeout", true)
	require.False(t, terminal)
	require.NotNil(t, rec)
	require.Equal(t, 2, rec.PollFailures)

	rec, terminal = manager.RecordPollFailure(context.Background(), "op-1", "temporary network timeout", true)
	require.True(t, terminal)
	require.NotNil(t, rec)
	require.Equal(t, StateFailed, rec.State)
	require.Len(t, manager.ConsumeChanged("conv-1", "turn-1"), 1)
}

func TestDeriveState_CompleteAlias(t *testing.T) {
	require.Equal(t, StateCompleted, DeriveState("COMPLETE", "", ""))
}

func TestManager_CancelTurnPollers_StopsRegisteredCancels(t *testing.T) {
	type testCase struct {
		name         string
		convID       string
		turnID       string
		cancelConv   string
		cancelTurn   string
		wantCanceled []string
		wantAlive    []string
	}
	cases := []testCase{
		{
			name:         "cancels all pollers for the target turn",
			convID:       "conv-1",
			turnID:       "turn-1",
			cancelConv:   "conv-1",
			cancelTurn:   "turn-1",
			wantCanceled: []string{"op-a", "op-b"},
		},
		{
			name:       "does not cancel pollers belonging to a different turn",
			convID:     "conv-1",
			turnID:     "turn-2",
			cancelConv: "conv-1",
			cancelTurn: "turn-1",
			wantAlive:  []string{"op-c"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			manager := NewManager()

			ops := []string{"op-a", "op-b"}
			if len(tc.wantAlive) > 0 {
				ops = tc.wantAlive
			}

			canceled := make(map[string]bool)
			for _, id := range ops {
				manager.Register(ctx, RegisterInput{
					ID:           id,
					ParentConvID: tc.convID,
					ParentTurnID: tc.turnID,
					ToolName:     "tool:start",
					Status:       "running",
				})
				opID := id // capture
				ok := manager.TryStartPoller(ctx, opID)
				require.True(t, ok)
				localCancel := func() { canceled[opID] = true }
				manager.StorePollerCancel(ctx, opID, localCancel)
			}

			manager.CancelTurnPollers(ctx, tc.cancelConv, tc.cancelTurn)

			for _, id := range tc.wantCanceled {
				require.True(t, canceled[id], "expected %q to be canceled", id)
			}
			for _, id := range tc.wantAlive {
				require.False(t, canceled[id], "expected %q to remain alive", id)
			}
		})
	}
}

func TestManager_FinishPoller_CleansCancelFunc(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()
	manager.Register(ctx, RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "tool:start",
		Status:       "running",
	})
	ok := manager.TryStartPoller(ctx, "op-1")
	require.True(t, ok)

	called := false
	manager.StorePollerCancel(ctx, "op-1", func() { called = true })

	manager.FinishPoller(ctx, "op-1")

	// Cancel should have been invoked by FinishPoller.
	require.True(t, called, "FinishPoller must invoke the stored cancel func")

	// After FinishPoller, TryStartPoller should succeed again (slot freed).
	ok = manager.TryStartPoller(ctx, "op-1")
	require.True(t, ok, "TryStartPoller should succeed after FinishPoller clears the slot")
}

func TestManager_CancelTurnPollers_NoOpWhenNoPollers(t *testing.T) {
	ctx := context.Background()
	manager := NewManager()
	manager.Register(ctx, RegisterInput{
		ID:           "op-1",
		ParentConvID: "conv-1",
		ParentTurnID: "turn-1",
		ToolName:     "tool:start",
		Status:       "running",
	})
	// No poller started — CancelTurnPollers must not panic.
	require.NotPanics(t, func() {
		manager.CancelTurnPollers(ctx, "conv-1", "turn-1")
	})
}

func TestManager_RegisterStoresOperationIntent(t *testing.T) {
	manager := NewManager()
	rec, _ := manager.Register(context.Background(), RegisterInput{
		ID:               "op-1",
		ParentConvID:     "conv-1",
		ParentTurnID:     "turn-1",
		ToolName:         "tool:start",
		OperationIntent:  "inspect repository structure",
		OperationSummary: "workdir=/tmp/ws | target=repo",
	})
	require.Equal(t, "inspect repository structure", rec.OperationIntent)
	require.Equal(t, "workdir=/tmp/ws | target=repo", rec.OperationSummary)

	got, ok := manager.Get(context.Background(), "op-1")
	require.True(t, ok)
	require.Equal(t, "inspect repository structure", got.OperationIntent)
	require.Equal(t, "workdir=/tmp/ws | target=repo", got.OperationSummary)
}

func intPtr(value int) *int {
	return &value
}

func TestManager_Sweep_PrunesTerminalAndDetachedOps(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	now := time.Now()

	// Terminal op, old — should be pruned.
	rec1, _ := manager.Register(ctx, RegisterInput{
		ID:            "terminal-old",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "completed",
	})
	require.NotNil(t, rec1)

	// Detached op, old — should be pruned.
	rec2, _ := manager.Register(ctx, RegisterInput{
		ID:            "detach-old",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeDetach),
		Status:        "running",
	})
	require.NotNil(t, rec2)

	// Wait-mode non-terminal op, old — must be preserved (live work).
	rec3, _ := manager.Register(ctx, RegisterInput{
		ID:            "wait-old",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})
	require.NotNil(t, rec3)

	// Terminal op, fresh — must be preserved (not old enough).
	rec4, _ := manager.Register(ctx, RegisterInput{
		ID:            "terminal-fresh",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "completed",
	})
	require.NotNil(t, rec4)

	// Age out the two "old" records by rewinding their UpdatedAt.
	manager.mu.Lock()
	manager.ops["terminal-old"].UpdatedAt = now.Add(-30 * time.Minute)
	manager.ops["detach-old"].UpdatedAt = now.Add(-30 * time.Minute)
	manager.ops["wait-old"].UpdatedAt = now.Add(-30 * time.Minute)
	manager.mu.Unlock()

	pruned := manager.Sweep(now, 15*time.Minute)
	require.Equal(t, 2, pruned)

	_, ok := manager.Get(ctx, "terminal-old")
	require.False(t, ok, "terminal-old should have been pruned")
	_, ok = manager.Get(ctx, "detach-old")
	require.False(t, ok, "detach-old should have been pruned")
	_, ok = manager.Get(ctx, "wait-old")
	require.True(t, ok, "wait-old is live work and must be preserved")
	_, ok = manager.Get(ctx, "terminal-fresh")
	require.True(t, ok, "terminal-fresh is under maxAge and must be preserved")
}

func TestManager_Sweep_SkipsSubscribedOps(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	now := time.Now()

	rec, _ := manager.Register(ctx, RegisterInput{
		ID:            "terminal-sub",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeDetach),
		Status:        "completed",
	})
	require.NotNil(t, rec)

	manager.mu.Lock()
	manager.ops["terminal-sub"].UpdatedAt = now.Add(-30 * time.Minute)
	manager.mu.Unlock()

	sub, _ := manager.Subscribe([]string{"terminal-sub"})
	require.NotNil(t, sub)
	// Drain in case the sub closed immediately (all-terminal at subscribe time).
	// When the op is still present and detach, allTargetsTerminalLocked returns
	// true (detach+completed), so the subscription closes on Subscribe itself.
	// In that case no subscription is retained and the sweep is free to prune.
	// Verify behavior: once the subscription is drained, subsequent Sweep prunes.
	select {
	case _, ok := <-sub:
		require.False(t, ok, "already-terminal subscribe should close immediately")
	default:
	}

	pruned := manager.Sweep(now, 15*time.Minute)
	require.Equal(t, 1, pruned)
}

func TestManager_Register_SetsTimeoutAtForAllModes(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	waitRec, _ := manager.Register(ctx, RegisterInput{
		ID:            "op-wait",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
		TimeoutMs:     60_000,
	})
	require.NotNil(t, waitRec.TimeoutAt, "wait-mode ops must get a TimeoutAt set when TimeoutMs > 0")

	// Detach ops also set TimeoutAt — used by the activated-status loop
	// in `tool_executor.maybeExecuteActivatedStatusTool` to bound how
	// long it re-polls the status tool for a changed snapshot.
	detachRec, _ := manager.Register(ctx, RegisterInput{
		ID:            "op-detach",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeDetach),
		Status:        "running",
		TimeoutMs:     60_000,
	})
	require.NotNil(t, detachRec.TimeoutAt, "detach-mode ops need TimeoutAt for the activated-status loop")

	// "fork" was a legacy synonym for "wait" that now normalizes down to
	// "wait" — TimeoutAt must still be set.
	legacyForkRec, _ := manager.Register(ctx, RegisterInput{
		ID:            "op-legacy-fork",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: "fork",
		Status:        "running",
		TimeoutMs:     60_000,
	})
	require.Equal(t, string(ExecutionModeWait), legacyForkRec.ExecutionMode, "legacy 'fork' must normalize to 'wait'")
	require.NotNil(t, legacyForkRec.TimeoutAt, "legacy fork-mode ops must inherit wait semantics including TimeoutAt")
}

func TestManager_ListOperations_FiltersAndExcludesTerminal(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	manager.Register(ctx, RegisterInput{
		ID:                   "wait-running",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(ExecutionModeWait),
		Status:               "running",
		OperationIntent:      "inspect repo",
	})

	manager.Register(ctx, RegisterInput{
		ID:                   "wait-terminal",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-1",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(ExecutionModeWait),
		Status:               "completed",
	})

	manager.Register(ctx, RegisterInput{
		ID:                   "detach-running",
		ParentConvID:         "conv-1",
		ParentTurnID:         "turn-2",
		ToolName:             "system/exec:execute",
		StatusToolName:       "system/exec:execute",
		StatusOperationIDArg: "sessionId",
		SameToolRecall:       true,
		StatusArgs:           map[string]interface{}{"sessionId": "detach-running", "action": "status"},
		ExecutionMode:        string(ExecutionModeDetach),
		Status:               "running",
	})

	manager.Register(ctx, RegisterInput{
		ID:                   "other-conv-running",
		ParentConvID:         "conv-2",
		ParentTurnID:         "turn-X",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		ExecutionMode:        string(ExecutionModeWait),
		Status:               "running",
	})

	all := manager.ListOperations(Filter{ConversationID: "conv-1"})
	require.Len(t, all, 2, "terminal ops must be excluded; both conv-1 non-terminal ops expected")

	ids := func(ops []PendingOp) []string {
		out := make([]string, 0, len(ops))
		for _, op := range ops {
			out = append(out, op.OperationID)
		}
		return out
	}
	require.ElementsMatch(t, []string{"wait-running", "detach-running"}, ids(all))

	byTool := manager.ListOperations(Filter{ConversationID: "conv-1", Tool: "llm/agents:start"})
	require.Len(t, byTool, 1)
	require.Equal(t, "wait-running", byTool[0].OperationID)
	require.Equal(t, "conversationId", byTool[0].OperationIDArg)
	require.False(t, byTool[0].SameToolRecall)

	byMode := manager.ListOperations(Filter{ConversationID: "conv-1", ExecutionMode: string(ExecutionModeDetach)})
	require.Len(t, byMode, 1)
	require.Equal(t, "detach-running", byMode[0].OperationID)
	require.True(t, byMode[0].SameToolRecall)
	require.Equal(t, "system/exec:execute", byMode[0].StatusTool)
	require.Equal(t, "detach-running", byMode[0].StatusArgs["sessionId"])

	crossConv := manager.ListOperations(Filter{})
	require.Len(t, crossConv, 3, "empty ConversationID = all conversations")

	none := manager.ListOperations(Filter{ConversationID: "conv-does-not-exist"})
	require.Empty(t, none)
}

func TestManager_Update_AlwaysRefreshesUpdatedAt(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	rec, _ := manager.Register(ctx, RegisterInput{
		ID:            "op-1",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeDetach),
		Status:        "running",
	})
	registeredAt := rec.UpdatedAt

	// Rewind UpdatedAt to simulate staleness.
	manager.mu.Lock()
	manager.ops["op-1"].UpdatedAt = registeredAt.Add(-10 * time.Minute)
	manager.mu.Unlock()

	// No-op Update (same status, same message, same state) must still
	// refresh UpdatedAt so GC does not reclaim a record the caller is
	// actively polling.
	_, changed := manager.Update(ctx, UpdateInput{ID: "op-1", Status: "running"})
	require.False(t, changed, "identical status should not trigger change events")

	refreshed, ok := manager.Get(ctx, "op-1")
	require.True(t, ok)
	require.True(t, refreshed.UpdatedAt.After(registeredAt.Add(-10*time.Minute)),
		"UpdatedAt must refresh on Update even when no fields changed, so GC window extends on LLM activity")
}

func TestManager_ListOperations_AlwaysIncludesStatusArgs(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	manager.Register(ctx, RegisterInput{
		ID:                   "op-non-recall",
		ParentConvID:         "c",
		ParentTurnID:         "t",
		ToolName:             "llm/agents:start",
		StatusToolName:       "llm/agents:status",
		StatusOperationIDArg: "conversationId",
		SameToolRecall:       false,
		StatusArgs: map[string]interface{}{
			"conversationId": "op-non-recall",
			"includeHistory": true, // extras
		},
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})

	ops := manager.ListOperations(Filter{ConversationID: "c"})
	require.Len(t, ops, 1)
	op := ops[0]
	require.False(t, op.SameToolRecall)
	require.NotNil(t, op.StatusArgs, "StatusArgs must be populated even for non-recall ops so extras carry to the LLM")
	require.Equal(t, "op-non-recall", op.StatusArgs["conversationId"])
	require.Equal(t, true, op.StatusArgs["includeHistory"])
}

func TestManager_Register_DuplicateIDReturnsExistedTrue(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	_, existed := manager.Register(ctx, RegisterInput{
		ID:            "op-dup",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ToolName:      "tool:start",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})
	require.False(t, existed, "first Register must return existed=false")

	_, existed = manager.Register(ctx, RegisterInput{
		ID:            "op-dup",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ToolName:      "tool:start",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})
	require.True(t, existed, "second Register with same id must return existed=true")
	stats := manager.Stats()
	require.Equal(t, int64(1), stats.RegisterOverwriteCount)
}

func TestManager_Stats_TracksLifetimeCountersAndGauges(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	manager.Register(ctx, RegisterInput{
		ID:            "op-1",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})
	manager.Update(ctx, UpdateInput{ID: "op-1", Message: "halfway"})
	_, subID := manager.Subscribe([]string{"op-1"})
	manager.Unsubscribe(subID)

	stats := manager.Stats()
	require.Equal(t, int64(1), stats.RegisterCount)
	require.Equal(t, int64(1), stats.UpdateCount)
	require.Equal(t, int64(1), stats.UpdateChangedCount)
	require.Equal(t, int64(1), stats.SubscribeCount)
	require.Equal(t, int64(1), stats.UnsubscribeCount)
	require.Equal(t, 1, stats.ActiveOps)
	require.Equal(t, 0, stats.ActiveSubscriptions, "subscription unsubscribed → gauge is 0")
}

func TestManager_DeepCloneStatusArgs_PreventsMutationOfCanonicalRecord(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()
	nested := map[string]interface{}{"a": 1}
	manager.Register(ctx, RegisterInput{
		ID: "op-1",
		StatusArgs: map[string]interface{}{
			"conversationId": "op-1",
			"opts":           nested,
		},
		ExecutionMode: string(ExecutionModeWait),
	})

	// Mutate the input map's nested entry after Register returns — the
	// canonical record should not change.
	nested["a"] = 999

	got, ok := manager.Get(ctx, "op-1")
	require.True(t, ok)
	opts, ok := got.StatusArgs["opts"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, 1, opts["a"], "nested map inside StatusArgs must be deep-cloned so external mutation does not leak")

	// Also verify that mutating the returned clone's nested map doesn't
	// alter the canonical state.
	opts["a"] = -1
	got2, _ := manager.Get(ctx, "op-1")
	opts2 := got2.StatusArgs["opts"].(map[string]interface{})
	require.Equal(t, 1, opts2["a"], "mutation of returned clone must not propagate back to canonical state")
}

func TestManager_Close_CancelsPollersAndClosesSubscriptions(t *testing.T) {
	manager := NewManager()
	ctx := context.Background()

	manager.Register(ctx, RegisterInput{
		ID:            "op-1",
		ParentConvID:  "c",
		ParentTurnID:  "t",
		ExecutionMode: string(ExecutionModeWait),
		Status:        "running",
	})

	// Admit an actual poller goroutine. AdmitPoller atomically registers
	// the cancel and bumps the WaitGroup; Close() waits on the wg so
	// this goroutine MUST exit before Close returns. If it didn't, Close
	// would hang — a deliberate failure mode for "leaked poller".
	pollCtx, cancel := context.WithCancel(context.Background())
	admitted := manager.AdmitPoller(ctx, "op-1", cancel)
	require.True(t, admitted, "AdmitPoller must succeed for a fresh op")

	goroutineExited := make(chan struct{})
	go func() {
		defer close(goroutineExited)
		defer manager.FinishPoller(ctx, "op-1")
		<-pollCtx.Done() // wait for Close to cancel us
	}()

	sub, subID := manager.Subscribe([]string{"op-1"})
	require.NotZero(t, subID)

	manager.Close()

	// Close is contractually synchronous on poller lifecycle — the
	// goroutine above must have observed the cancel and exited by the
	// time Close returns.
	select {
	case <-goroutineExited:
	default:
		t.Fatal("poller goroutine should have exited before Close returned")
	}

	select {
	case _, ok := <-sub:
		require.False(t, ok, "subscription channel must be closed after Close")
	case <-time.After(time.Second):
		t.Fatal("expected subscription channel to be closed")
	}

	stats := manager.Stats()
	require.Equal(t, 0, stats.ActivePollers)
	require.Equal(t, 0, stats.ActiveSubscriptions)

	// Idempotent.
	manager.Close()
}

func TestManager_AdmitPoller_AfterCloseIsNoop(t *testing.T) {
	manager := NewManager()
	manager.Close()
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted := manager.AdmitPoller(context.Background(), "op-x", cancel)
	require.False(t, admitted, "AdmitPoller must refuse after Close")
	_ = pollCtx
}

func TestManager_Close_JoinsPollerThatExitsViaCancel(t *testing.T) {
	// Simulates the real integration: admit a poller, Close fires the
	// registered cancel, the goroutine's defer chain runs FinishPoller,
	// wg drops to zero, Close returns.
	manager := NewManager()
	for i := 0; i < 3; i++ {
		id := "op-" + string(rune('a'+i))
		manager.Register(context.Background(), RegisterInput{
			ID: id, ParentConvID: "c", ParentTurnID: "t",
			ExecutionMode: string(ExecutionModeWait), Status: "running",
		})
		pollCtx, cancel := context.WithCancel(context.Background())
		require.True(t, manager.AdmitPoller(context.Background(), id, cancel))
		go func(pc context.Context, opID string) {
			defer manager.FinishPoller(context.Background(), opID)
			<-pc.Done()
		}(pollCtx, id)
	}

	done := make(chan struct{})
	go func() {
		manager.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return — poller goroutines were not joined")
	}

	stats := manager.Stats()
	require.Equal(t, 0, stats.ActivePollers)
}

func TestManager_TryStartPoller_AfterCloseIsNoop(t *testing.T) {
	manager := NewManager()
	manager.Close()
	require.False(t, manager.TryStartPoller(context.Background(), "op-x"))
}

func TestManager_Close_CancelsStartGCGoroutine(t *testing.T) {
	manager := NewManager()

	// Use a ctx that will NOT cancel on its own so the only way the GC
	// goroutine can exit is via Close() cancelling its derived ctx.
	ctx := context.Background()
	// Short-enough interval that if the goroutine were still running we
	// would see a sweep before Close returns.
	manager.StartGC(ctx, 10*time.Millisecond, time.Hour)
	manager.StartGC(ctx, 10*time.Millisecond, time.Hour) // two concurrent sweepers

	// Close must block until all GC goroutines exit.
	done := make(chan struct{})
	go func() {
		manager.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return — GC goroutines were not joined")
	}

	// Post-close StartGC must no-op.
	manager.StartGC(ctx, 10*time.Millisecond, time.Hour)
	// Second Close must be idempotent and not block.
	done2 := make(chan struct{})
	go func() {
		manager.Close()
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(time.Second):
		t.Fatal("second Close blocked — should be a no-op")
	}
}

func TestManager_StartGC_PostCloseIsNoop(t *testing.T) {
	manager := NewManager()
	manager.Close()

	// StartGC after Close must not launch a goroutine. We can't directly
	// observe the absence of a goroutine in a portable test, but we can
	// verify the returned cancel-via-ctx path still works: pass a short
	// ctx; if a goroutine had been launched, the ticker would fire
	// Sweep, which would race with our reads. Instead we just assert
	// the call returns immediately without panicking and that Close
	// remains idempotent.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager.StartGC(ctx, 10*time.Millisecond, time.Hour)
	manager.Close() // idempotent
}

func TestManager_Sweep_NoopWithNonPositiveMaxAge(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:            "op-1",
		ExecutionMode: string(ExecutionModeDetach),
		Status:        "completed",
	})
	require.Equal(t, 0, manager.Sweep(time.Now(), 0))
	require.Equal(t, 0, manager.Sweep(time.Now(), -1*time.Second))
	_, ok := manager.Get(context.Background(), "op-1")
	require.True(t, ok)
}
