package async

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManager_RegisterWaitConsume(t *testing.T) {
	manager := NewManager()
	rec := manager.Register(context.Background(), RegisterInput{
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "system/exec:start",
		WaitForResponse: true,
		Status:          "running",
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
	rec := manager.Register(context.Background(), RegisterInput{
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

func TestManager_TryRecordReinforcement_RateLimited(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:                            "op-1",
		ParentConvID:                  "conv-1",
		ParentTurnID:                  "turn-1",
		ToolName:                      "system/exec:start",
		Status:                        "running",
		MaxReinforcementsPerOperation: 1,
		MinIntervalBetweenMs:          60000,
	})
	_, ok := manager.TryRecordReinforcement(context.Background(), "op-1")
	require.True(t, ok)
	rec, ok := manager.TryRecordReinforcement(context.Background(), "op-1")
	require.False(t, ok)
	require.NotNil(t, rec)
	require.Equal(t, 1, rec.ReinforcementCount)
}

func TestManager_FindActiveByRequest(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:                "op-1",
		ParentConvID:      "conv-1",
		ParentTurnID:      "turn-1",
		ToolName:          "forecasting:TotalV1",
		RequestArgsDigest: `{"viewId":"TOTAL"}`,
		WaitForResponse:   true,
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
		ID:              "op-1",
		ParentConvID:    "conv-1",
		ParentTurnID:    "turn-1",
		ToolName:        "forecasting:TotalV1",
		WaitForResponse: true,
		PollIntervalMs:  50,
		Status:          "WAITING",
	})

	started := time.Now()
	err := manager.WaitForNextPoll(context.Background(), "conv-1", "turn-1")
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(started), 40*time.Millisecond)
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

func TestManager_RegisterStoresInstructionFields(t *testing.T) {
	type testCase struct {
		name                string
		instruction         string
		terminalInstruction string
	}
	cases := []testCase{
		{
			name:                "both fields set",
			instruction:         "Do not call start again.",
			terminalInstruction: "Answer from child result.",
		},
		{
			name:                "only Instruction set",
			instruction:         "Poll status tool next.",
			terminalInstruction: "",
		},
		{
			name:                "only TerminalInstruction set",
			instruction:         "",
			terminalInstruction: "Use cached result.",
		},
		{
			name:                "neither set",
			instruction:         "",
			terminalInstruction: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			manager := NewManager()
			rec := manager.Register(context.Background(), RegisterInput{
				ID:                  "op-1",
				ParentConvID:        "conv-1",
				ParentTurnID:        "turn-1",
				ToolName:            "llm/agents:start",
				Status:              "running",
				Instruction:         tc.instruction,
				TerminalInstruction: tc.terminalInstruction,
			})
			require.NotNil(t, rec)
			require.Equal(t, tc.instruction, rec.Instruction)
			require.Equal(t, tc.terminalInstruction, rec.TerminalInstruction)

			// Verify the values survive Get (clone path).
			got, ok := manager.Get(context.Background(), "op-1")
			require.True(t, ok)
			require.Equal(t, tc.instruction, got.Instruction)
			require.Equal(t, tc.terminalInstruction, got.TerminalInstruction)
		})
	}
}

func TestManager_InstructionNotMutatedByClone(t *testing.T) {
	manager := NewManager()
	manager.Register(context.Background(), RegisterInput{
		ID:                  "op-1",
		ParentConvID:        "conv-1",
		ParentTurnID:        "turn-1",
		ToolName:            "system/exec:start",
		Status:              "running",
		Instruction:         "original instruction",
		TerminalInstruction: "original terminal",
	})

	rec1, _ := manager.Get(context.Background(), "op-1")
	rec2, _ := manager.Get(context.Background(), "op-1")

	// Mutating the clone must not affect the stored record.
	rec1.Instruction = "mutated"
	require.Equal(t, "original instruction", rec2.Instruction, "clone mutation should not propagate")
}

func intPtr(value int) *int {
	return &value
}
