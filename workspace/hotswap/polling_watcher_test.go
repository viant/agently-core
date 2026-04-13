package hotswap

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/viant/agently-core/workspace"
)

// fakeStore is a minimal workspace.Store for testing the polling watcher.
type fakeStore struct {
	mu      sync.Mutex
	entries map[string][]workspace.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: make(map[string][]workspace.Entry)}
}

func (f *fakeStore) Root() string { return "fake://" }

func (f *fakeStore) List(_ context.Context, kind string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, e := range f.entries[kind] {
		names = append(names, e.Name)
	}
	return names, nil
}

func (f *fakeStore) Load(_ context.Context, kind, name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entries[kind] {
		if e.Name == name {
			return e.Data, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) Save(_ context.Context, _, _ string, _ []byte) error { return nil }
func (f *fakeStore) Delete(_ context.Context, _, _ string) error         { return nil }
func (f *fakeStore) Exists(_ context.Context, _, _ string) (bool, error) { return false, nil }

func (f *fakeStore) Entries(_ context.Context, kind string) ([]workspace.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]workspace.Entry{}, f.entries[kind]...), nil
}

func (f *fakeStore) setEntries(kind string, entries []workspace.Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[kind] = entries
}

func TestPollingWatcher_DetectChanges(t *testing.T) {
	store := newFakeStore()
	now := time.Now()

	// Seed initial state with one entry.
	store.setEntries("agents", []workspace.Entry{
		{Kind: "agents", Name: "a1", UpdatedAt: now},
	})

	watcher := NewPollingWatcher(store, WithPollInterval(10*time.Millisecond))

	var mu sync.Mutex
	var changes []Change

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = watcher.Watch(ctx, []string{"agents"}, func(ch Change) {
			mu.Lock()
			changes = append(changes, ch)
			mu.Unlock()
		})
	}()

	// PollingWatcher builds its initial snapshot inside Watch before the first
	// ticker cycle. Give the goroutine one short interval to establish that
	// baseline before mutating the store.
	time.Sleep(25 * time.Millisecond)

	// Add a new entry and update existing.
	store.setEntries("agents", []workspace.Entry{
		{Kind: "agents", Name: "a1", UpdatedAt: now.Add(time.Second)},
		{Kind: "agents", Name: "a2", UpdatedAt: now},
	})

	waitUntil(t, 300*time.Millisecond, 10*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		if len(changes) < 2 {
			return false
		}
		foundA1, foundA2 := false, false
		for _, ch := range changes {
			if ch.Name == "a1" && ch.Action == ActionAddOrUpdate {
				foundA1 = true
			}
			if ch.Name == "a2" && ch.Action == ActionAddOrUpdate {
				foundA2 = true
			}
		}
		return foundA1 && foundA2
	}, "expected update for a1 and add for a2")

	mu.Lock()
	snapshot := append([]Change{}, changes...)
	mu.Unlock()

	// We should see at least an update for a1 and an add for a2.
	if len(snapshot) < 2 {
		t.Fatalf("expected at least 2 changes, got %d: %v", len(snapshot), snapshot)
	}

	foundA1, foundA2 := false, false
	for _, ch := range snapshot {
		if ch.Name == "a1" && ch.Action == ActionAddOrUpdate {
			foundA1 = true
		}
		if ch.Name == "a2" && ch.Action == ActionAddOrUpdate {
			foundA2 = true
		}
	}
	if !foundA1 {
		t.Error("expected update change for a1")
	}
	if !foundA2 {
		t.Error("expected add change for a2")
	}

	// Now remove a2 to test deletion detection.
	mu.Lock()
	changes = nil
	mu.Unlock()

	store.setEntries("agents", []workspace.Entry{
		{Kind: "agents", Name: "a1", UpdatedAt: now.Add(time.Second)},
	})

	waitUntil(t, 300*time.Millisecond, 10*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ch := range changes {
			if ch.Name == "a2" && ch.Action == ActionDelete {
				return true
			}
		}
		return false
	}, "expected delete change for a2")

	mu.Lock()
	snapshot = append([]Change{}, changes...)
	mu.Unlock()

	foundDelete := false
	for _, ch := range snapshot {
		if ch.Name == "a2" && ch.Action == ActionDelete {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Error("expected delete change for a2")
	}

	cancel()
	<-done
}
