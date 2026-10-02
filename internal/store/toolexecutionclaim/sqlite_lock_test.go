package toolexecutionclaim

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	moderncsqlite "modernc.org/sqlite"
	sqlitecode "modernc.org/sqlite/lib"
)

func TestIsSQLiteLock_ClassifiesModerncBusyAndRejectsOtherErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "claim-lock.sqlite")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec("PRAGMA busy_timeout = 1"); err != nil {
			t.Fatal(err)
		}
		return db
	}

	writerDB := open()
	contenderDB := open()
	if _, err := writerDB.Exec("CREATE TABLE claims (claim_key TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	transaction, err := writerDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	if _, err := transaction.Exec("INSERT INTO claims(claim_key) VALUES ('held')"); err != nil {
		t.Fatal(err)
	}
	_, lockErr := contenderDB.Exec("INSERT INTO claims(claim_key) VALUES ('contender')")
	if lockErr == nil {
		t.Fatal("expected competing write to report SQLITE_BUSY")
	}
	var sqliteErr *moderncsqlite.Error
	if !errors.As(lockErr, &sqliteErr) {
		t.Fatalf("expected modernc SQLite error, got %T: %v", lockErr, lockErr)
	}
	if !isSQLiteLock(fmt.Errorf("claim write: %w", lockErr)) {
		t.Fatalf("wrapped modernc SQLITE_BUSY was not classified as retryable: code=%d error=%v", sqliteErr.Code(), lockErr)
	}

	if _, err := contenderDB.Exec("INSERT INTO missing_claims(claim_key) VALUES ('invalid')"); err == nil {
		t.Fatal("expected missing-table SQLite error")
	} else if isSQLiteLock(err) {
		t.Fatalf("non-busy SQLite error was classified as retryable: %v", err)
	}
	if isSQLiteLock(errors.New("database is locked")) {
		t.Fatal("plain text error must not be classified as a SQLite lock")
	}
	if isSQLiteLock(nil) {
		t.Fatal("nil must not be classified as a SQLite lock")
	}
}

func TestIsSQLiteLock_ClassifiesModerncExtendedBusySnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "claim-busy-snapshot.sqlite")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", dbPath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec("PRAGMA busy_timeout = 1"); err != nil {
			t.Fatal(err)
		}
		return db
	}

	readerDB := open()
	writerDB := open()
	var journalMode string
	if err := readerDB.QueryRow("PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode=%q, want wal", journalMode)
	}
	if _, err := readerDB.Exec("CREATE TABLE claims (claim_key TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	transaction, err := readerDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	var count int
	if err := transaction.QueryRow("SELECT COUNT(*) FROM claims").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := writerDB.Exec("INSERT INTO claims(claim_key) VALUES ('newer-snapshot')"); err != nil {
		t.Fatal(err)
	}
	_, lockErr := transaction.Exec("INSERT INTO claims(claim_key) VALUES ('stale-snapshot')")
	if lockErr == nil {
		t.Fatal("expected a stale WAL snapshot to return SQLITE_BUSY_SNAPSHOT")
	}
	var sqliteErr *moderncsqlite.Error
	if !errors.As(lockErr, &sqliteErr) {
		t.Fatalf("expected modernc SQLite error, got %T: %v", lockErr, lockErr)
	}
	if sqliteErr.Code() != sqlitecode.SQLITE_BUSY_SNAPSHOT {
		t.Fatalf("SQLite code=%d, want SQLITE_BUSY_SNAPSHOT (%d): %v", sqliteErr.Code(), sqlitecode.SQLITE_BUSY_SNAPSHOT, lockErr)
	}
	if !isSQLiteLock(fmt.Errorf("claim write: %w", lockErr)) {
		t.Fatalf("extended SQLITE_BUSY_SNAPSHOT was not classified as retryable: %v", lockErr)
	}
}
