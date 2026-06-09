package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/viant/agently-core/app/store/data"
	asynccfg "github.com/viant/agently-core/protocol/async"
)

type fakeSignalCounter struct {
	controller    int
	approvals     int
	elicitations  int
	controllerErr error
	approvalsErr  error
	elicErr       error
}

func (f *fakeSignalCounter) CountControllerTurns(_ context.Context, _ string, _ ...data.Option) (int, error) {
	return f.controller, f.controllerErr
}

func (f *fakeSignalCounter) CountPendingApprovals(_ context.Context, _ string, _ ...data.Option) (int, error) {
	return f.approvals, f.approvalsErr
}

func (f *fakeSignalCounter) CountPendingElicitations(_ context.Context, _ string, _ ...data.Option) (int, error) {
	return f.elicitations, f.elicErr
}

func TestGatherControllerSignals(t *testing.T) {
	ctx := context.Background()

	t.Run("truthful values", func(t *testing.T) {
		manager := asynccfg.NewManager()
		manager.Register(ctx, asynccfg.RegisterInput{
			ID:            "op-1",
			ParentConvID:  "c1",
			ParentTurnID:  "t1",
			ToolName:      "llm/agents:start",
			ExecutionMode: string(asynccfg.ExecutionModeWait),
			Status:        "running",
		})
		got := gatherControllerSignals(ctx, &fakeSignalCounter{controller: 3, approvals: 2, elicitations: 1}, manager, "c1")
		if !got.PendingElicitation {
			t.Fatalf("PendingElicitation = false, want true")
		}
		if !got.PendingApproval {
			t.Fatalf("PendingApproval = false, want true")
		}
		if !got.PendingAsync {
			t.Fatalf("PendingAsync = false, want true")
		}
		if got.AutonomousTurnsUsed != 3 {
			t.Fatalf("AutonomousTurnsUsed = %d, want 3", got.AutonomousTurnsUsed)
		}
	})

	t.Run("zero counts yield conservative defaults", func(t *testing.T) {
		got := gatherControllerSignals(ctx, &fakeSignalCounter{}, nil, "c1")
		if got.PendingElicitation || got.PendingApproval || got.PendingAsync || got.AutonomousTurnsUsed != 0 {
			t.Fatalf("expected all-zero signals, got %+v", got)
		}
	})

	t.Run("query errors degrade safely", func(t *testing.T) {
		got := gatherControllerSignals(ctx, &fakeSignalCounter{
			controller:    5,
			approvals:     5,
			elicitations:  5,
			controllerErr: errors.New("boom"),
			approvalsErr:  errors.New("boom"),
			elicErr:       errors.New("boom"),
		}, nil, "c1")
		if got.PendingElicitation || got.PendingApproval || got.PendingAsync || got.AutonomousTurnsUsed != 0 {
			t.Fatalf("errors should yield zero signals, got %+v", got)
		}
	})

	t.Run("incapable data service yields zero signals", func(t *testing.T) {
		got := gatherControllerSignals(ctx, struct{}{}, nil, "c1")
		if got.PendingElicitation || got.PendingApproval || got.PendingAsync || got.AutonomousTurnsUsed != 0 {
			t.Fatalf("expected zero signals for incapable service, got %+v", got)
		}
	})
}
