package sqlitewrite

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_SerializesByKey(t *testing.T) {
	const key = "sqlite:test"
	var concurrent int32
	var maxConcurrent int32
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	run := func() error {
		_, err := Do(context.Background(), key, func() (struct{}, error) {
			n := atomic.AddInt32(&concurrent, 1)
			defer atomic.AddInt32(&concurrent, -1)
			for {
				cur := atomic.LoadInt32(&maxConcurrent)
				if n <= cur || atomic.CompareAndSwapInt32(&maxConcurrent, cur, n) {
					break
				}
			}
			started <- struct{}{}
			<-release
			return struct{}{}, nil
		})
		return err
	}

	errCh := make(chan error, 2)
	go func() { errCh <- run() }()
	<-started
	go func() { errCh <- run() }()

	select {
	case <-started:
		t.Fatalf("second writer entered before first released")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("first Do() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("second Do() error = %v", err)
	}
	if got := atomic.LoadInt32(&maxConcurrent); got != 1 {
		t.Fatalf("max concurrent = %d, want 1", got)
	}
}

func TestDo_RespectsContextWhileWaiting(t *testing.T) {
	const key = "sqlite:test-timeout"
	block := make(chan struct{})

	go func() {
		_, _ = Do(context.Background(), key, func() (struct{}, error) {
			<-block
			return struct{}{}, nil
		})
	}()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := Do(ctx, key, func() (struct{}, error) { return struct{}{}, nil })
	close(block)
	if err == nil {
		t.Fatalf("expected context error while waiting on gate")
	}
}
