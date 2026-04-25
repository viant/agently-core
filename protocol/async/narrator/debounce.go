package narrator

import (
	"sync"
	"time"
)

// Debouncer collapses a stream of text updates into one emission per
// window. Safe for concurrent use by multiple goroutines — all state
// mutations are guarded by mu so callers do not need external
// synchronization.
//
// Typical use from a single goroutine (e.g. the barrier's select loop)
// is still the common case, but the internal mutex means
// `go test -race` cannot report a false positive if the caller
// accidentally invokes Push / Flush / Channel from more than one
// goroutine.
type Debouncer struct {
	mu      sync.Mutex
	window  time.Duration
	timer   *time.Timer
	pending string
	active  bool
}

func NewDebouncer(window time.Duration) *Debouncer {
	return &Debouncer{window: window}
}

// Push records a new pending value. When the debounce window is zero,
// the input is passed through immediately (synchronous emission).
// Otherwise the first Push starts the window and returns ""; subsequent
// Push calls within the window replace `pending` and also return "".
// Callers drain via Channel() + Flush() when the timer fires.
func (d *Debouncer) Push(text string) string {
	if d == nil {
		return text
	}
	if text == "" {
		return ""
	}
	if d.window <= 0 {
		return text
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.active {
		d.timer = time.NewTimer(d.window)
		d.active = true
		d.pending = text
		return ""
	}
	d.pending = text
	return ""
}

// Channel returns the debounce timer's channel, or nil when no window
// is currently active. Safe to call concurrently with Push / Flush.
//
// The returned channel is the *timer's* channel; receiving on it is
// safe even if Flush races in parallel and stops the timer. A stopped
// timer's channel simply never fires again. Callers typically re-read
// Channel() on each select iteration so they pick up the current state.
func (d *Debouncer) Channel() <-chan time.Time {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.active || d.timer == nil {
		return nil
	}
	return d.timer.C
}

// Flush returns the pending value (if any) and clears debouncer state.
// Stops and drains the internal timer. Safe to call at any time; a
// Flush with no pending value returns "".
func (d *Debouncer) Flush() string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	text := d.pending
	d.pending = ""
	if d.timer != nil {
		if !d.timer.Stop() {
			select {
			case <-d.timer.C:
			default:
			}
		}
		d.timer = nil
	}
	d.active = false
	return text
}
