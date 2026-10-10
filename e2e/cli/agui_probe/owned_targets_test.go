package main

import "testing"

func TestOwnedTargetsRejectNonExactLoopback(t *testing.T) {
	for _, value := range []string{"https://127.0.0.1:29501", "http://localhost:29501", "http://external.invalid:29501", "http://user:secret@127.0.0.1:29501", "http://127.0.0.1:29501/path", "http://127.0.0.1:29501?target=other", "http://127.0.0.1:29501#fragment", "http://127.0.0.1", "http://127.0.0.1:80"} {
		if _, err := ownedTargets(value); err == nil {
			t.Errorf("accepted unowned target")
		}
	}
	targets, err := ownedTargets("http://127.0.0.1:29501", "http://127.0.0.1:29502")
	if err != nil || len(targets) != 2 || targets["http://127.0.0.1:29383"] {
		t.Fatal("exact target boundary lost")
	}
}
