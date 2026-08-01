package executionprotection

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	toolprotection "github.com/viant/agently-core/protocol/tool/protection"
)

func TestDAORepositoryUsesStandardPersistenceAndDefersAvailabilityFailure(t *testing.T) {
	if standardConnectorName != "agently" {
		t.Fatalf("standard connector = %q, want agently", standardConnectorName)
	}
	repository := NewDAORepository(nil)
	if repository == nil {
		t.Fatal("NewDAORepository(nil) = nil")
	}
	_, err := repository.Claim(context.Background(), ClaimRecord{
		ClaimKey:     strings.Repeat("a", 64),
		SemanticHash: strings.Repeat("b", 64),
		CreatedAt:    time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("Claim() with unavailable standard persistence error = nil")
	}
	if err := repository.Finish(context.Background(), strings.Repeat("a", 64), toolprotection.StateCompleted, time.Now().UTC()); err == nil {
		t.Fatal("Finish() with unavailable standard persistence error = nil")
	}
}

func TestRepositoryDriverAndDuplicateClassification(t *testing.T) {
	for _, driver := range []string{"sqlite", "sqlite3", "SQLITE"} {
		if !isSQLiteDriver(driver) {
			t.Fatalf("isSQLiteDriver(%q) = false", driver)
		}
	}
	if isSQLiteDriver("mysql") {
		t.Fatal("MySQL classified as SQLite")
	}

	duplicate := &mysql.MySQLError{Number: 1062, Message: "duplicate"}
	if !isMySQLDuplicate(duplicate, "mysql") {
		t.Fatal("MySQL 1062 was not recognized")
	}
	if isMySQLDuplicate(duplicate, "sqlite") {
		t.Fatal("MySQL 1062 was recognized for SQLite")
	}
	if isMySQLDuplicate(&mysql.MySQLError{Number: 1061}, "mysql") {
		t.Fatal("non-1062 MySQL error was recognized as duplicate")
	}
}
