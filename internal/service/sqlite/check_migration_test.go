package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	iscript "github.com/viant/agently-core/internal/script"
)

func TestStripSQLiteChecks(t *testing.T) {
	for _, tc := range []struct {
		name, ddl string
		count     int
	}{
		{"column", `CREATE TABLE t (id INTEGER CHECK (id IN (0,1)), name TEXT NOT NULL)`, 1},
		{"named column", `CREATE TABLE t (id INTEGER CONSTRAINT "odd check" CHECK ((id+abs(id))>=0) NOT NULL)`, 1},
		{"table first", `CREATE TABLE t (CHECK (id > 0), id INTEGER)`, 1},
		{"table last", `CREATE TABLE t (id INTEGER, CONSTRAINT [old name] CHECK (id IN (1,2))) STRICT`, 1},
		{"multiple", `CREATE TABLE t (id INTEGER CHECK (id>0) CHECK (id<9), CHECK (id IN (1,2)), CONSTRAINT c CHECK (id<>0))`, 4},
		{"quoted", "CREATE TABLE t (`CHECK` TEXT DEFAULT 'CHECK (x), ''quoted'' )', \"x)\" INTEGER CHECK (\"x)\">0))", 1},
		{"comments", "CREATE TABLE t (id INTEGER /* CHECK (fake) */ CHECK (id IN (1, /* ) */ 2)), -- CHECK (fake)\n name TEXT)", 1},
		{"unchanged", `CREATE TABLE t (checkpoint TEXT DEFAULT 'CHECK (x)', "CHECK" INTEGER, x INTEGER DEFAULT (abs(-1)))`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ddl, count, err := stripSQLiteChecks(tc.ddl)
			if err != nil || count != tc.count {
				t.Fatalf("count=%d err=%v DDL=%s", count, err, ddl)
			}
			if tc.count == 0 && ddl != tc.ddl {
				t.Fatal("unconstrained DDL changed")
			}
			_, remaining, err := stripSQLiteChecks(ddl)
			if err != nil || remaining != 0 {
				t.Fatalf("CHECK remains: %d %v", remaining, err)
			}
			db := newCheckTestDB(t)
			if _, err := db.Exec(ddl); err != nil {
				t.Fatalf("invalid transformed DDL: %v\n%s", err, ddl)
			}
		})
	}
	for _, ddl := range []string{"CREATE TABLE t (x CHECK ((x>0))", "CREATE TABLE t (x TEXT DEFAULT 'unterminated)", "CREATE TABLE t (x /* unfinished"} {
		if _, _, err := stripSQLiteChecks(ddl); err == nil {
			t.Fatalf("malformed DDL accepted: %s", ddl)
		}
	}
}

func newCheckTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "checks.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func execCheckSQL(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func assertNoSQLiteChecks(t *testing.T, db *sql.DB) {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tables, err := sqliteCheckTables(context.Background(), conn)
	if err != nil || len(tables) != 0 {
		t.Fatalf("constrained tables remain: %v %v", tables, err)
	}
}

func TestSQLiteCheckMigrationPreservesSchemaAndData(t *testing.T) {
	db := newCheckTestDB(t)
	execCheckSQL(t, db, `
CREATE TABLE "parent odd" (id TEXT PRIMARY KEY, flag INTEGER NOT NULL DEFAULT 0 CONSTRAINT "different name" CHECK (flag IN (0,1))) WITHOUT ROWID, STRICT;
CREATE TABLE child (id INTEGER PRIMARY KEY AUTOINCREMENT, parent TEXT NOT NULL REFERENCES "parent odd"(id) ON DELETE RESTRICT,
 state TEXT NOT NULL UNIQUE CHECK (state IN ('ready','done')), doubled INTEGER GENERATED ALWAYS AS (id*2) STORED,
 CONSTRAINT arbitrary CHECK (length(state)>0));
CREATE TABLE legacy_schedule (id TEXT PRIMARY KEY, enabled INTEGER CHECK (enabled IN (0,1)), checkpoint TEXT DEFAULT 'CHECK (ignored)');
CREATE TABLE audit (value TEXT);
CREATE TABLE __agently_no_checks_0 (value TEXT);
CREATE INDEX child_expression ON child(lower(state)) WHERE parent IS NOT NULL;
CREATE VIEW child_view AS SELECT id,state,doubled FROM child;
CREATE VIEW dependent_view AS SELECT * FROM child_view;
CREATE TRIGGER child_audit AFTER INSERT ON child BEGIN INSERT INTO audit VALUES(new.state); END;
CREATE TRIGGER parent_audit AFTER UPDATE ON "parent odd" BEGIN INSERT INTO audit SELECT state FROM child; END;
INSERT INTO "parent odd" VALUES('p',1);
INSERT INTO child(id,parent,state) VALUES(7,'p','ready'),(100,'p','done');
DELETE FROM child WHERE id=100;
INSERT INTO legacy_schedule(rowid,id,enabled) VALUES(900,'legacy',1);
DELETE FROM audit;`)
	for i := 0; i < 2; i++ {
		if err := removeSQLiteChecks(context.Background(), db); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
		assertNoSQLiteChecks(t, db)
	}
	var id, doubled, count int
	var state string
	if err := db.QueryRow("SELECT id,state,doubled FROM dependent_view").Scan(&id, &state, &doubled); err != nil || id != 7 || state != "ready" || doubled != 14 {
		t.Fatalf("data or dependent views changed: %d %s %d %v", id, state, doubled, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit").Scan(&count); err != nil || count != 0 {
		t.Fatalf("copy fired application triggers: %d %v", count, err)
	}
	if err := db.QueryRow("SELECT rowid FROM legacy_schedule").Scan(&id); err != nil || id != 900 {
		t.Fatalf("rowid changed: %d %v", id, err)
	}
	execCheckSQL(t, db, "UPDATE \"parent odd\" SET flag=9", "UPDATE legacy_schedule SET enabled=9", "INSERT INTO child(parent,state) VALUES('p','future')")
	if err := db.QueryRow("SELECT id,doubled FROM child WHERE state='future'").Scan(&id, &doubled); err != nil || id != 101 || doubled != 202 {
		t.Fatalf("sequence or generated column changed: %d %d %v", id, doubled, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit").Scan(&count); err != nil || count != 2 {
		t.Fatalf("triggers not restored: %d %v", count, err)
	}
	for _, stmt := range []string{
		"INSERT INTO child(parent,state) VALUES('absent','orphan')",
		"INSERT INTO child(parent,state) VALUES('p','ready')",
		"INSERT INTO child(parent,state) VALUES('p',NULL)",
		"DELETE FROM \"parent odd\" WHERE id='p'",
		"INSERT INTO \"parent odd\" VALUES('strict','not an integer')",
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("non-CHECK constraint lost: %s", stmt)
		}
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_schema WHERE name='child_expression'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("expression index lost: %v", err)
	}
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign keys not restored: %d %v", enabled, err)
	}
}

func TestSQLiteCheckMigrationRollback(t *testing.T) {
	for _, failure := range []string{"foreign key", "shadowed rowid"} {
		t.Run(failure, func(t *testing.T) {
			db := newCheckTestDB(t)
			db.SetMaxOpenConns(1)
			execCheckSQL(t, db, "CREATE TABLE first (id INTEGER PRIMARY KEY CHECK(id>0)); INSERT INTO first VALUES(1); CREATE VIEW first_view AS SELECT * FROM first;")
			if failure == "foreign key" {
				execCheckSQL(t, db, "PRAGMA foreign_keys=OFF", "CREATE TABLE child (p INTEGER REFERENCES first(id)); INSERT INTO child VALUES(99)", "PRAGMA foreign_keys=ON")
			} else {
				execCheckSQL(t, db, "CREATE TABLE zlast (rowid TEXT, _rowid_ TEXT, oid TEXT, x INTEGER CHECK(x>0))")
			}
			var before string
			if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='first'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if err := removeSQLiteChecks(context.Background(), db); err == nil {
				t.Fatal("migration should fail")
			}
			var after string
			if err := db.QueryRow("SELECT sql FROM sqlite_schema WHERE name='first'").Scan(&after); err != nil || after != before {
				t.Fatalf("DDL not rolled back: %s %v", after, err)
			}
			var id, enabled int
			if err := db.QueryRow("SELECT id FROM first_view").Scan(&id); err != nil || id != 1 {
				t.Fatalf("data/view not rolled back: %d %v", id, err)
			}
			if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
				t.Fatalf("foreign keys not restored after error: %d %v", enabled, err)
			}
		})
	}
}

func TestSQLiteBootstrapAndLegacyChecks(t *testing.T) {
	for _, tc := range []struct {
		name, ddl string
	}{
		{"bootstrap", iscript.SqlListScript},
		{"legacy", preProtocolSchema},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newCheckTestDB(t)
			execCheckSQL(t, db, tc.ddl)
			if tc.name == "legacy" {
				if !strings.Contains(tc.ddl, "CHECK (") {
					t.Fatal("legacy fixture must retain CHECKs")
				}
				if err := applyCompatibilityMigrations(context.Background(), db); err != nil {
					t.Fatal(err)
				}
			}
			assertNoSQLiteChecks(t, db)
		})
	}
	for _, memory := range []bool{false, true} {
		svc := New(t.TempDir())
		var dsn string
		var err error
		if memory {
			dsn, err = svc.EnsureInMemory(context.Background())
		} else {
			dsn, err = svc.Ensure(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		assertNoSQLiteChecks(t, db)
		execCheckSQL(t, db, "INSERT INTO conversation(id,shareable) VALUES('check-free',7)")
		execCheckSQL(t, db, "DELETE FROM conversation WHERE id='check-free'")
		db.Close()
	}
}

func TestSQLiteCheckMigrationCancelledAndOriginalFKPolicy(t *testing.T) {
	db := newCheckTestDB(t)
	db.SetMaxOpenConns(1)
	execCheckSQL(t, db, "CREATE TABLE t (id INTEGER CHECK(id>0)); INSERT INTO t VALUES(1)")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := removeSQLiteChecks(ctx, db); err == nil {
		t.Fatal("cancelled migration succeeded")
	}
	if _, err := db.Exec("INSERT INTO t VALUES(-1)"); err == nil {
		t.Fatal("cancelled migration modified schema")
	}
	execCheckSQL(t, db, "PRAGMA foreign_keys=OFF")
	if err := removeSQLiteChecks(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("original FK policy not restored: %d %v", enabled, err)
	}
	assertNoSQLiteChecks(t, db)
}
