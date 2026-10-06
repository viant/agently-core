package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
	runmodel "github.com/viant/agently-core/model/run"
)

type protocolWatchdogStore struct {
	data.Service
	failure error
	stop    context.CancelFunc
}

func (s *protocolWatchdogStore) ListStaleRuns(context.Context, *runmodel.StaleRunsInput, ...data.Option) ([]*runmodel.StaleRunsView, error) {
	s.stop()
	return nil, s.failure
}
func TestWatchdogProtocolRecoveryRunsIndependentlyOfNativeEmptyOrErrorSweep(t *testing.T) {
	for _, failure := range []error{nil, errors.New("native discovery unavailable")} {
		ctx, stop := context.WithCancel(context.Background())
		calls := atomic.Int32{}
		store := &protocolWatchdogStore{failure: failure, stop: stop}
		watchdog := NewWatchdog(store, nil, WithWatchdogInterval(time.Hour), WithWatchdogProtocolRecovery(func(context.Context) error { calls.Add(1); return nil }, nil))
		watchdog.Start(ctx)
		if calls.Load() != 1 {
			t.Fatalf("protocol callback suppressed by native sweep: %v", failure)
		}
	}
}
func TestWatchdogProtocolRecoveryGuardPreservesOtherNativeRecovery(t *testing.T) {
	calls := 0
	watchdog := NewWatchdog(nil, nil, WithWatchdogProtocolRecovery(nil, func(_ context.Context, _ string, turn string) (bool, error) { return turn == "approval-boundary", nil }))
	watchdog.handleFn = func(context.Context, *runmodel.StaleRunsView) error { calls++; return nil }
	for _, turn := range []string{"approval-boundary", "initial", "native-started"} {
		conversation := "thread"
		if err := watchdog.handleRun(context.Background(), &runmodel.StaleRunsView{Id: turn, TurnId: &turn, ConversationId: &conversation}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("guard suppressed unrelated native recovery: calls=%d", calls)
	}
}
