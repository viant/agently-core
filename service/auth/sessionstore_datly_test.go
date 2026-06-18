package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
	sessionwrite "github.com/viant/agently-core/pkg/agently/user/session/write"
)

func TestSessionStoreDAO_Get_PrefersFriendlyUserIdentity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	if _, err := sessionwrite.DefineComponent(ctx, dao); err != nil {
		t.Fatalf("DefineComponent() error = %v", err)
	}

	users := NewDatlyUserService(dao)
	if users == nil {
		t.Fatalf("NewDatlyUserService() = nil")
	}
	userID, err := users.UpsertWithProvider(ctx, "awitas", "Awitas", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("UpsertWithProvider() error = %v", err)
	}
	if userID == "" {
		t.Fatalf("UpsertWithProvider() returned empty userID")
	}

	store := NewSessionStoreDAO(dao)
	rec := &SessionRecord{
		ID:        "sess-friendly",
		UserID:    userID,
		Provider:  "session",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}
	conn, err := dao.Resource().Connector("agently")
	if err != nil {
		t.Fatalf("Connector() error = %v", err)
	}
	db, err := conn.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO session(id, user_id, provider, created_at, updated_at, expires_at) VALUES(?, ?, ?, ?, ?, ?)`,
		rec.ID,
		rec.UserID,
		rec.Provider,
		rec.CreatedAt,
		sql.NullTime{},
		rec.ExpiresAt,
	); err != nil {
		t.Fatalf("insert session error = %v", err)
	}

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("store.Get() error = %v", err)
	}
	if got == nil {
		t.Fatalf("store.Get() = nil")
	}
	if got.Username != "Awitas" {
		t.Fatalf("got.Username = %q, want %q", got.Username, "Awitas")
	}
	if got.Email != "awitas@viantinc.com" {
		t.Fatalf("got.Email = %q, want %q", got.Email, "awitas@viantinc.com")
	}
	if got.Subject != "awitas_viant_devtest" {
		t.Fatalf("got.Subject = %q, want %q", got.Subject, "awitas_viant_devtest")
	}
}

func TestSessionStoreDAO_ManagerPutIgnoresCanceledCallerContext(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	if _, err := sessionwrite.DefineComponent(ctx, dao); err != nil {
		t.Fatalf("DefineComponent() error = %v", err)
	}

	users := NewDatlyUserService(dao)
	userID, err := users.UpsertWithProvider(ctx, "awitas", "Awitas", "awitas@viantinc.com", "oauth", "awitas_viant_devtest")
	if err != nil {
		t.Fatalf("UpsertWithProvider() error = %v", err)
	}
	if userID == "" {
		t.Fatalf("UpsertWithProvider() returned empty userID")
	}

	store := NewSessionStoreDAO(dao)
	manager := NewManager(time.Hour, store)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.Put(canceledCtx, &Session{
		ID:        "sess-canceled-datly-write",
		UserID:    userID,
		Username:  "awitas",
		Email:     "awitas@viantinc.com",
		Subject:   "awitas_viant_devtest",
		Provider:  "oauth",
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})

	conn, err := dao.Resource().Connector("agently")
	if err != nil {
		t.Fatalf("Connector() error = %v", err)
	}
	db, err := conn.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	var gotUserID string
	if err := db.QueryRowContext(ctx, `SELECT user_id FROM session WHERE id = ?`, "sess-canceled-datly-write").Scan(&gotUserID); err != nil {
		t.Fatalf("session was not persisted after canceled caller context: %v", err)
	}
	if gotUserID != userID {
		t.Fatalf("session user_id = %q, want %q", gotUserID, userID)
	}
}
