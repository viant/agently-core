package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestInMemorySQLiteRetainsSharedCacheWithoutFileWriterPolicy(t *testing.T) {
	dsn, err := New(t.TempDir()).EnsureInMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dsn, "mode=memory&cache=shared") || strings.Contains(dsn, "_txlock=") || strings.Contains(dsn, "journal_mode(WAL)") {
		t.Fatalf("in-memory policy changed: %s", dsn)
	}
}

// PRAGMAs on the temporary schema connection do not configure later Datly pool
// connections. Exercise multiple independent connections using only the DSN.
func TestProvisionedFileConnectionsEnforceNativeForeignKeys(t *testing.T) {
	ctx := context.Background()
	dsn, err := New(t.TempDir()).Ensure(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(3)
	var connections []*sql.Conn
	for i := 0; i < 3; i++ {
		c, e := db.Conn(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer c.Close()
		connections = append(connections, c)
		var enabled int
		if e = c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); e != nil {
			t.Fatal(e)
		}
		if enabled != 1 {
			t.Fatalf("connection %d foreign keys disabled", i)
		}
	}
	c := connections[2]
	if _, err = c.ExecContext(ctx, "INSERT INTO turn(id,conversation_id,status) VALUES('orphan','missing','pending')"); err == nil {
		t.Fatal("orphan native turn accepted")
	}
	for _, statement := range []string{
		"INSERT INTO conversation(id) VALUES('native-parent')",
		"INSERT INTO turn(id,conversation_id,status) VALUES('native-turn','native-parent','completed')",
		"INSERT INTO run(id,conversation_id,turn_id,status) VALUES('native-run','native-parent','native-turn','completed')",
		"INSERT INTO call_payload(id,run_id,kind,mime_type,size_bytes,storage,inline_body) VALUES('run-document','native-run','attachment','application/json',2,'inline','{}')",
		"DELETE FROM run WHERE id='native-run'",
	} {
		if _, err = c.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM call_payload WHERE id='run-document'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("native run payload did not cascade")
	}
}
