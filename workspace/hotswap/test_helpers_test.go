package hotswap

import (
	"testing"
	"time"
)

func waitUntil(t *testing.T, timeout, tick time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(tick)
	}
}

func assertNever(t *testing.T, waitFor, tick time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		if cond() {
			t.Fatal(msg)
		}
		time.Sleep(tick)
	}
}
