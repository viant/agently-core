package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/viant/agently-core/internal/testutil/dbtest"
)

func newSessionStoreNativeFixture(t *testing.T) (*SessionStoreNative, *sql.DB) {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "session-store-v1")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	_, err := db.Exec(`INSERT INTO users(id,username,display_name,email,provider,subject)
		VALUES('u1','localuser','Local User','user@example.test','oauth','oauth_subject_test')`)
	if err != nil {
		t.Fatal(err)
	}
	server := newMCPLinkTestNative(t, dbPath)
	return NewSessionStoreNative(server), db
}

func TestSessionStoreNative_FriendlyUserIdentityAndDelete(t *testing.T) {
	ctx := context.Background()
	store, _ := newSessionStoreNativeFixture(t)
	rec := &SessionRecord{
		ID: "sess-friendly", UserID: "u1", Provider: "session",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Username != "Local User" || got.Email != "user@example.test" || got.Subject != "oauth_subject_test" {
		t.Fatalf("joined session identity = %+v", got)
	}
	if err := store.Delete(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
	got, err = store.Get(ctx, rec.ID)
	if err != nil || got != nil {
		t.Fatalf("deleted session = %+v, %v", got, err)
	}
}

func TestSessionStoreNative_ManagerPutIgnoresCanceledCallerContext(t *testing.T) {
	ctx := context.Background()
	store, db := newSessionStoreNativeFixture(t)
	manager := NewManager(time.Hour, store)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	manager.Put(canceledCtx, &Session{
		ID: "sess-canceled-datly-write", UserID: "u1", Username: "localuser",
		Email: "user@example.test", Subject: "oauth_subject_test", Provider: "oauth",
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	var gotUserID string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM session WHERE id = ?`, "sess-canceled-datly-write").Scan(&gotUserID); err != nil {
		t.Fatalf("session was not persisted after canceled caller context: %v", err)
	}
	if gotUserID != "u1" {
		t.Fatalf("session user_id = %q, want u1", gotUserID)
	}
}
