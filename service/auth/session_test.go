package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingSessionStore struct {
	calls   atomic.Int32
	release chan struct{}
	record  *SessionRecord
}

func (c *countingSessionStore) Get(_ context.Context, _ string) (*SessionRecord, error) {
	c.calls.Add(1)
	<-c.release
	return c.record, nil
}

func (c *countingSessionStore) Upsert(_ context.Context, _ *SessionRecord) error { return nil }
func (c *countingSessionStore) Delete(_ context.Context, _ string) error         { return nil }

func TestSessionManagerGet_DedupesConcurrentStoreLoads(t *testing.T) {
	store := &countingSessionStore{
		release: make(chan struct{}),
		record: &SessionRecord{
			ID:        "sess-1",
			UserID:    "user-1",
			Username:  "awitas",
			Provider:  "oauth",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}
	manager := NewManager(time.Hour, store)

	var wg sync.WaitGroup
	results := make([]*Session, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = manager.Get(context.Background(), "sess-1")
		}(i)
	}

	time.Sleep(50 * time.Millisecond)
	close(store.release)
	wg.Wait()

	if got := store.calls.Load(); got != 1 {
		t.Fatalf("store.Get() calls = %d, want %d", got, 1)
	}
	for i, sess := range results {
		if sess == nil {
			t.Fatalf("results[%d] = nil", i)
		}
		if sess.ID != "sess-1" {
			t.Fatalf("results[%d].ID = %q, want %q", i, sess.ID, "sess-1")
		}
	}
}

type contextAwareSessionStore struct {
	called atomic.Int32
	record *SessionRecord
}

func (c *contextAwareSessionStore) Get(ctx context.Context, _ string) (*SessionRecord, error) {
	c.called.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.record, nil
}

func (c *contextAwareSessionStore) Upsert(_ context.Context, _ *SessionRecord) error { return nil }
func (c *contextAwareSessionStore) Delete(_ context.Context, _ string) error         { return nil }

func TestSessionManagerGet_LoadsFromStoreEvenWhenCallerContextIsCanceled(t *testing.T) {
	store := &contextAwareSessionStore{
		record: &SessionRecord{
			ID:        "sess-canceled",
			UserID:    "user-1",
			Username:  "awitas",
			Provider:  "oauth",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}
	manager := NewManager(time.Hour, store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sess := manager.Get(ctx, "sess-canceled")
	if sess == nil {
		t.Fatalf("manager.Get() = nil, want session")
	}
	if got := store.called.Load(); got != 1 {
		t.Fatalf("store.Get() calls = %d, want 1", got)
	}
	if sess.ID != "sess-canceled" {
		t.Fatalf("sess.ID = %q, want %q", sess.ID, "sess-canceled")
	}
}
