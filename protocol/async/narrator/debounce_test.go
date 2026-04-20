package narrator

import (
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
