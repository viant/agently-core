package schema

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed sqlite.sql
var sqliteDDL string

// SQLiteDDL is the unchanged reporting baseline used by the Core discovery
// fixture and by hosts that explicitly initialize a new reporting database.
func SQLiteDDL() string { return sqliteDDL }

// UpSQLite creates missing reporting tables in one SQLite transaction. The
// caller owns the database and must create its conversation table first.
func UpSQLite(ctx context.Context, db *sql.DB) error {
	if ctx == nil || ctx.Err() != nil || db == nil {
		return fmt.Errorf("reporting SQLite migration requires a live context and database")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, sqliteDDL); err != nil {
		return err
	}
	return tx.Commit()
}
