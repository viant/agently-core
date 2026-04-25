package narrator

import (
	"sync"
	"testing"
	"time"
)

func TestDebouncer_PushAndFlush(t *testing.T) {
	d := NewDebouncer(10 * time.Millisecond)
	if got := d.Push("a"); got != "" {
		t.Fatalf("first Push() = %q", got)
	}
	if got := d.Push("b"); got != "" {
		t.Fatalf("second Push() = %q", got)
	}
	if d.Channel() == nil {
		t.Fatal("expected debounce channel")
	}
	time.Sleep(12 * time.Millisecond)
	if got := d.Flush(); got != "b" {
		t.Fatalf("Flush() = %q, want %q", got, "b")
	}
	if d.Channel() != nil {
		t.Fatal("expected nil channel after flush")
	}
}

func TestDebouncer_ZeroWindowImmediate(t *testing.T) {
	d := NewDebouncer(0)
	if got := d.Push("a"); got != "a" {
		t.Fatalf("Push() = %q, want %q", got, "a")
	}
}

// TestDebouncer_ConcurrentPushFlushChannel exercises Push / Flush /
// Channel from multiple goroutines simultaneously so `go test -race`
// catches any unsynchronized access. It does not assert any particular
// emission ordering — the contract under concurrency is only "no data
// race" and "no panic." The eventually-final Flush always returns the
// current pending text or empty.
func TestDebouncer_ConcurrentPushFlushChannel(t *testing.T) {
	d := NewDebouncer(5 * time.Millisecond)
	var wg sync.WaitGroup

	const workers = 8
	const iterations = 200

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				d.Push("payload")
				_ = d.Channel()
				if i%17 == 0 {
					d.Flush()
				}
			}
		}(w)
	}

	wg.Wait()
	// Final Flush is also safe under races and must not panic.
	_ = d.Flush()
}
