package cohort

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	forgetypes "github.com/viant/forge/backend/types"
)

type pollFixture struct {
	syncs       atomic.Int32
	disconnects atomic.Int32
	complete    chan struct{}
}

func (p *pollFixture) Sync(context.Context) error {
	if p.syncs.Add(1) == 1 {
		return errors.New("transport unavailable")
	}
	select {
	case p.complete <- struct{}{}:
	default:
	}
	return nil
}
func (*pollFixture) EnsureAdmitted(context.Context) error { return nil }
func (*pollFixture) OpenWindowVariant(context.Context, string, *forgetypes.WindowTarget) (*forgetypes.WindowResourceVariant, string, error) {
	return nil, "", nil
}
func (p *pollFixture) MarkDisconnected() { p.disconnects.Add(1) }

func TestArtifactCohortPollFencesFailureAndReconciles(t *testing.T) {
	p := &pollFixture{complete: make(chan struct{}, 1)}
	stop := StartReconciliation(context.Background(), Binding{Source: p, PollInterval: time.Millisecond})
	defer stop()
	select {
	case <-p.complete:
	case <-time.After(time.Second):
		t.Fatal("conditional reconciliation did not retry")
	}
	if p.syncs.Load() < 2 || p.disconnects.Load() != 1 {
		t.Fatalf("sync=%d disconnect=%d", p.syncs.Load(), p.disconnects.Load())
	}
}

func TestArtifactCohortNotificationStreamSuppressesFallbackPoll(t *testing.T) {
	p := &pollFixture{complete: make(chan struct{}, 1)}
	stop := StartReconciliation(context.Background(), Binding{
		Source:       p,
		PollInterval: time.Millisecond,
		Start:        func(context.Context) error { return nil },
	})
	defer stop()
	time.Sleep(10 * time.Millisecond)
	if got := p.syncs.Load(); got != 0 {
		t.Fatalf("notification-backed source also polled in the background: %d sync calls", got)
	}
}
