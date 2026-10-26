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
			reg := &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok","data":[{"value":1}]}`}}}
			out, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "op", Name: "sample/MetricSource"}, conv)
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
func TestIndependentCallsDecorateTheirOwnDurableOperation(t *testing.T) {
	conv := &stubConv{}
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "independent-decorate-conv", TurnID: "independent-decorate-turn"})
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
	reg := &independentHeldRegistry{scriptedRegistry: &scriptedRegistry{script: []scriptedResult{{result: `{"status":"ok"}`}}}, started: make(chan struct{}, 2), release: make(chan struct{})}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	errors := make(chan error, 2)
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			out, _, e := ExecuteToolStep(ctx, reg, StepInfo{ID: id, Name: "sample/MetricSource", Args: map[string]interface{}{"date": "same"}}, conv)
			results <- out.Result
			errors <- e
		}(id)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-reg.started:
		case <-time.After(5 * time.Second):
			close(reg.release)
			wg.Wait()
			t.Fatal("both calls must execute independently")
		}
	}
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
	require.Equal(t, 2, reg.calls)
}

// Compile-time check that existing conversation clients are unaffected by the
// optional decoration hook; no new methods were added to the store interface.
var _ apiconv.Client = (*stubConv)(nil)

func TestUndecoratedToolResultPreservesOpaqueProducerPayload(t *testing.T) {
	body := `{"profile":"producer-defined-v9","measure":{"customUnits":17},"unknown":[null,false,{"value":"keep"}]}`
	ctx := memory.WithTurnMeta(context.Background(), memory.TurnMeta{ConversationID: "opaque-conversation", TurnID: "opaque-turn"})
	reg := &scriptedRegistry{script: []scriptedResult{{result: body}}}
	out, _, err := ExecuteToolStep(ctx, reg, StepInfo{ID: "opaque-op", Name: "sample/arbitrary"}, &stubConv{})
	require.NoError(t, err)
	require.Equal(t, body, out.Result)
}
