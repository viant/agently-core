package toolexec

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apiconv "github.com/viant/agently-core/app/store/conversation"
	memory "github.com/viant/agently-core/runtime/requestctx"
)

func TestDurableDecoratorRunsAfterFinalizationAndNeverOnPersistenceFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			conv := &stubConv{}
			if fail {
				conv.failPatchToolCallAt = map[int]error{2: fmt.Errorf("terminal unavailable"), 3: fmt.Errorf("terminal unavailable")}
			}
			calls := 0
			ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "decorator-conv", TurnID: "decorator-turn"})
			ctx = WithDurableResultDecorator(ctx, func(_ context.Context, step StepInfo, raw string) (string, bool, error) {
				calls++
				conv.mu.Lock()
				defer conv.mu.Unlock()
				completed := false
				for _, c := range conv.patchedToolCalls {
					if c.OpID == step.ID && c.Status == "completed" {
						completed = true
					}
				}
				if !completed {
					t.Fatal("receipt before durable completion")
				}
				return "receipt:" + step.ID, true, nil
			})
			reg := &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok","data":[{"avails":1}]}`}}}
			out, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "op", Name: "steward/ForecastingCube"}, conv)
			if fail {
				require.Error(t, err)
				require.Zero(t, calls)
				require.NotEqual(t, "receipt:op", out.Result)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, "receipt:op", out.Result)
			}
		})
	}
}
func TestCoalescedCallsDecorateTheirOwnDurableOperation(t *testing.T) {
	previous := activeToolStepCoalescer
	activeToolStepCoalescer = newToolStepCoalescer(5 * time.Second)
	t.Cleanup(func() { activeToolStepCoalescer = previous })
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "coalesce-decorate-conv", TurnID: "coalesce-decorate-turn"})
	ctx = WithDurableResultDecorator(ctx, func(_ context.Context, step StepInfo, raw string) (string, bool, error) {
		conv.mu.Lock()
		defer conv.mu.Unlock()
		for _, c := range conv.patchedToolCalls {
			if c.OpID == step.ID && c.Status == "completed" {
				return "receipt:" + step.ID, true, nil
			}
		}
		return "", true, fmt.Errorf("own op incomplete")
	})
	reg := &heldCoalesceRegistry{scriptedRegistry: &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok"}`}}}, entered: make(chan struct{}), release: make(chan struct{})}
	waiter := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan string, 2)
	errors := make(chan error, 2)
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			callCtx := ctx
			if id == "second" {
				<-reg.entered
				callCtx = &coalesceWaiterContext{Context: ctx, entered: waiter}
			}
			out, _, e := ExecuteToolStep(callCtx, reg, StepInfo{ID: id, Name: "steward/ForecastingCube", Args: map[string]interface{}{"date": "same"}}, conv)
			results <- out.Result
			errors <- e
		}(id)
	}
	<-waiter
	close(reg.release)
	wg.Wait()
	close(results)
	close(errors)
	for e := range errors {
		require.NoError(t, e)
	}
	var got []string
	for r := range results {
		got = append(got, r)
	}
	require.ElementsMatch(t, []string{"receipt:first", "receipt:second"}, got)
}

// Compile-time check that existing conversation clients are unaffected by the
// optional decoration hook; no new methods were added to the store interface.
var _ apiconv.Client = (*stubConv)(nil)
