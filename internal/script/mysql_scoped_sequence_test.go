package script

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/sqlx/io/sequence"
)

// AGENTLY_TEST_MYSQL_DSN must name an isolated, explicitly provisioned test DB.
func TestMySQLScopedSequenceSchemaAndCallerRollback(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL fixture not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var engine, columnType, charset, collation string
	var length int
	var nullable string
	if err = db.QueryRowContext(ctx, "SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=?", sequence.LedgerTable).Scan(&engine); err != nil {
		t.Fatal("scoped ledger must be created by schema provisioning", err)
	}
	if err = db.QueryRowContext(ctx, "SELECT COLUMN_TYPE,CHARACTER_MAXIMUM_LENGTH,CHARACTER_SET_NAME,COLLATION_NAME,IS_NULLABLE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='scope_key'", sequence.LedgerTable).Scan(&columnType, &length, &charset, &collation, &nullable); err != nil {
		t.Fatal(err)
	}
	if engine != "InnoDB" || columnType != "char(64)" || length != 64 || charset != "ascii" || collation != "ascii_bin" || nullable != "NO" {
		t.Fatalf("incompatible scoped ledger schema: %s %s %d %s %s %s", engine, columnType, length, charset, collation, nullable)
	}
	if err = sequence.Provision(ctx, db, "mysql"); err != nil {
		t.Fatal("generic reprovision must be idempotent", err)
	}
	table := fmt.Sprintf("agently_scope_test_%d", time.Now().UnixNano())
	if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+"(id VARCHAR(32) PRIMARY KEY,turn_id VARCHAR(32),sequence BIGINT,UNIQUE(turn_id,sequence)) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(context.Background(), "DROP TABLE IF EXISTS "+table)
	if _, err = db.ExecContext(ctx, "INSERT INTO "+table+" VALUES('first','turn-a',7),('other','turn-b',100)"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		turn           string
		supplied, want []int64
	}{{"turn-a", []int64{9}, []int64{8, 10}}, {"turn-b", nil, []int64{101}}} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		values, err := sequence.Reserve(ctx, tx, sequence.Request{Dialect: "mysql", Table: table, Column: "sequence", Scope: []sequence.Scope{{Column: "turn_id", Value: tc.turn}}, Count: len(tc.want), Supplied: tc.supplied})
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, tc.want) {
			tx.Rollback()
			t.Fatalf("scope %s values %v want %v", tc.turn, values, tc.want)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES('pending',?,?)", tc.turn, values[0]); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 2 {
		t.Fatalf("caller rollback lost: rows %d error %v", count, err)
	}
}
