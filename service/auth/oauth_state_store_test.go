package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly/bootstrap/connector"
)

// newLinkStateStore opens the stock v1 host against one disposable workspace
// database. Schema inspection below is fixture-only SQL.
func newLinkStateStore(t *testing.T) (*OAuthStateStoreNative, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..")
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "oauth-state-v1")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	server, err := native.New(ctx, native.Options{
		SourceRoot: project,
		Connectors: []connector.Config{{Name: "agently", Driver: "sqlite3", DSN: dbPath + "?_foreign_keys=on&_busy_timeout=5000", MaxOpenConns: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	store := NewOAuthStateStoreNative(server)
	if store == nil {
		t.Fatal("native oauth state store is nil")
	}
	return store, db
}

func TestOAuthLinkStateMigration_SQLiteTableExists(t *testing.T) {
	ctx := context.Background()
	_, db := newLinkStateStore(t)
	row := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM oauth_link_state`)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("oauth_link_state table missing from SQLite schema: %v", err)
	}
	var indexName string
	if err := db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='ux_oauth_link_state_flow'`).Scan(&indexName); err != nil {
		t.Fatalf("ux_oauth_link_state_flow index missing: %v", err)
	}
}

func stateRecord(state, flow, user, session string, expiresAt time.Time) *OAuthStateRecord {
	return &OAuthStateRecord{
		StateHash:       state,
		FlowHash:        flow,
		CanonicalUserID: user,
		SessionHash:     session,
		Provider:        "adelphic-dev6",
		ExpiresAt:       expiresAt,
	}
}

func TestOAuthStateStore_CreateOwnerAndPending(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(7 * time.Minute)

	stored, created, err := store.CreateOrGetPending(ctx, stateRecord("state-1", "flow-1", "user-1", "sess-1", expiry))
	if err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	if !created {
		t.Fatalf("first CreateOrGetPending() created = false, want true")
	}
	if stored == nil || stored.StateHash != "state-1" {
		t.Fatalf("stored = %+v, want state-1", stored)
	}

	// A concurrent caller (any pod) must not become an owner and must observe
	// the owner's row.
	stored, created, err = store.CreateOrGetPending(ctx, stateRecord("state-2", "flow-1", "user-1", "sess-1", expiry))
	if err != nil {
		t.Fatalf("second CreateOrGetPending() error = %v", err)
	}
	if created {
		t.Fatalf("second CreateOrGetPending() created = true, want false (pending)")
	}
	if stored == nil || stored.StateHash != "state-1" {
		t.Fatalf("pending caller observed %+v, want the owner row state-1", stored)
	}

	// Status polling sees the pending flow.
	pending, err := store.GetPending(ctx, "flow-1")
	if err != nil || pending == nil || pending.StateHash != "state-1" {
		t.Fatalf("GetPending() = %+v, %v; want state-1 row", pending, err)
	}
}

func TestOAuthStateStore_ConsumeSemantics(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(7 * time.Minute)
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-c", "flow-c", "user-1", "sess-1", expiry)); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}

	// Cross-user consume is rejected before the owner consumes.
	if err := store.Consume(ctx, "state-c", "user-2", "sess-1"); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("cross-user Consume() = %v, want ErrOAuthStateInvalid", err)
	}
	// Cross-session consume is rejected.
	if err := store.Consume(ctx, "state-c", "user-1", "sess-2"); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("cross-session Consume() = %v, want ErrOAuthStateInvalid", err)
	}
	// Owner consume succeeds exactly once.
	if err := store.Consume(ctx, "state-c", "user-1", "sess-1"); err != nil {
		t.Fatalf("owner Consume() error = %v", err)
	}
	// Replay is rejected.
	if err := store.Consume(ctx, "state-c", "user-1", "sess-1"); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("replayed Consume() = %v, want ErrOAuthStateInvalid", err)
	}
	// Absent state is rejected with the same non-enumerable error.
	if err := store.Consume(ctx, "state-missing", "user-1", "sess-1"); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("absent Consume() = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestOAuthStateStore_ExpiredConsumeRejected(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	expired := time.Now().UTC().Add(-time.Minute)
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-e", "flow-e", "user-1", "sess-1", expired)); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	if err := store.Consume(ctx, "state-e", "user-1", "sess-1"); !errors.Is(err, ErrOAuthStateInvalid) {
		t.Fatalf("expired Consume() = %v, want ErrOAuthStateInvalid", err)
	}
}

func TestOAuthStateStore_ReplacesConsumedAndExpiredFlows(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(7 * time.Minute)
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-r1", "flow-r", "user-1", "sess-1", expiry)); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	if err := store.Consume(ctx, "state-r1", "user-1", "sess-1"); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	// A consumed flow row never permanently blocks later linking: the next
	// initiation replaces it with a new state hash.
	stored, created, err := store.CreateOrGetPending(ctx, stateRecord("state-r2", "flow-r", "user-1", "sess-1", expiry))
	if err != nil {
		t.Fatalf("replace CreateOrGetPending() error = %v", err)
	}
	if !created || stored == nil || stored.StateHash != "state-r2" {
		t.Fatalf("replace = (%v, %+v), want created=true state-r2", created, stored)
	}
	if stored.ConsumedAt != nil {
		t.Fatalf("replaced row still consumed: %+v", stored)
	}

	// An expired flow row is replaced the same way.
	expiredStore, _ := newLinkStateStore(t)
	if _, _, err := expiredStore.CreateOrGetPending(ctx, stateRecord("state-x1", "flow-x", "user-1", "sess-1", time.Now().UTC().Add(-time.Minute))); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	stored, created, err = expiredStore.CreateOrGetPending(ctx, stateRecord("state-x2", "flow-x", "user-1", "sess-1", expiry))
	if err != nil {
		t.Fatalf("expired replace error = %v", err)
	}
	if !created || stored == nil || stored.StateHash != "state-x2" {
		t.Fatalf("expired replace = (%v, %+v), want created=true state-x2", created, stored)
	}
}

func TestOAuthStateStore_DeleteExpired(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-d1", "flow-d1", "user-1", "sess-1", now.Add(-10*time.Minute))); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-d2", "flow-d2", "user-1", "sess-1", now.Add(10*time.Minute))); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	// The shared in-memory schema may carry expired rows from sibling tests;
	// assert the targeted rows rather than an exact global count.
	deleted, oldest, err := store.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	if deleted < 1 {
		t.Fatalf("DeleteExpired() deleted = %d, want >= 1", deleted)
	}
	if oldest.IsZero() || !oldest.Before(now) {
		t.Fatalf("DeleteExpired() oldest = %v, want a pre-horizon expiry", oldest)
	}
	// The live flow remains pending.
	pending, err := store.GetPending(ctx, "flow-d2")
	if err != nil || pending == nil {
		t.Fatalf("GetPending(flow-d2) = %+v, %v; want pending row", pending, err)
	}
	if pending2, _ := store.GetPending(ctx, "flow-d1"); pending2 != nil {
		t.Fatalf("GetPending(flow-d1) = %+v, want nil after cleanup", pending2)
	}
}

func TestOAuthStateStore_GetPendingExcludesConsumed(t *testing.T) {
	store, _ := newLinkStateStore(t)
	ctx := context.Background()
	expiry := time.Now().UTC().Add(7 * time.Minute)
	if _, _, err := store.CreateOrGetPending(ctx, stateRecord("state-p", "flow-p", "user-1", "sess-1", expiry)); err != nil {
		t.Fatalf("CreateOrGetPending() error = %v", err)
	}
	if err := store.Consume(ctx, "state-p", "user-1", "sess-1"); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	pending, err := store.GetPending(ctx, "flow-p")
	if err != nil {
		t.Fatalf("GetPending() error = %v", err)
	}
	if pending != nil {
		t.Fatalf("GetPending() = %+v, want nil for consumed flow", pending)
	}
}
