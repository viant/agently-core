package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestService_EnsureInMemory(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='conversation'").Scan(&name); err != nil {
		t.Fatalf("conversation table missing: %v", err)
	}
	if name != "conversation" {
		t.Fatalf("unexpected table name: %s", name)
	}
}
