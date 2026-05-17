package auth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
)

func TestSessionStoreDAO_Get_PrefersFriendlyUserIdentity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
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
