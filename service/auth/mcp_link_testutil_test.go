package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	"github.com/viant/datly/bootstrap/connector"
	"github.com/viant/datly/standalone"
)

// newMCPLinkTestDBWithPath provisions only an isolated schema fixture.
func newMCPLinkTestDBWithPath(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "mcp-link-auth")
	t.Cleanup(cleanup)
	dbtest.LoadSQLiteSchema(t, db)
	return db, dbPath
}

func newMCPLinkTestNative(t *testing.T, dbPath string) *standalone.Server {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Join(filepath.Dir(file), "..", "..")
	server, err := native.New(context.Background(), native.Options{
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
	return server
}

func authTokenTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
