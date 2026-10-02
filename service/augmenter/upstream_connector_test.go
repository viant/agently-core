package augmenter

import (
	"context"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestUpstreamConnectorOwnership(t *testing.T) {
	ctx := context.Background()
	s := New(nil)
	db, err := s.openUpstream(ctx, "sqlite3", "file:upstream-owned?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.openUpstream(ctx, "sqlite3", "file:upstream-owned?mode=memory&cache=shared")
	if err != nil || again != db {
		t.Fatalf("resolved target did not reuse pool: %v", err)
	}
	other, err := s.openUpstream(ctx, "sqlite3", "file:upstream-other?mode=memory&cache=shared")
	if err != nil || other == db {
		t.Fatalf("distinct targets shared a pool: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.openUpstream(canceled, "sqlite3", "file:upstream-owned?mode=memory&cache=shared"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call returned %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if db.PingContext(ctx) == nil || other.PingContext(ctx) == nil {
		t.Fatal("service close left owned pool open")
	}
	if _, err = s.openUpstream(ctx, "sqlite3", "file:upstream-owned?mode=memory&cache=shared"); err == nil {
		t.Fatal("closed service reopened an upstream")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}
