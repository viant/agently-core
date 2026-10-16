package schema

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"strings"
)

// mysql.sql preserves the deployed Steward Skeema reporting definitions for
// both agently schemas. The host conversation table is a prerequisite.
//
//go:embed mysql.sql
var mysqlDDL string

func MySQLDDL() string { return mysqlDDL }

// UpMySQL creates missing reporting tables in foreign-key order. MySQL DDL
// commits each statement; a retry is safe after a partial initialization.
// Existing tables are left as they are for the deployment migration owner.
func UpMySQL(ctx context.Context, db *sql.DB) error {
	if ctx == nil || ctx.Err() != nil || db == nil {
		return fmt.Errorf("reporting MySQL migration requires a live context and database")
	}
	const prefix = "CREATE TABLE `"
	count := 0
	for _, raw := range strings.Split(mysqlDDL, ";") {
		statement := strings.TrimSpace(raw)
		if statement == "" {
			continue
		}
		if !strings.HasPrefix(statement, prefix) {
			return fmt.Errorf("invalid embedded reporting MySQL migration")
		}
		statement = strings.Replace(statement, prefix, "CREATE TABLE IF NOT EXISTS `", 1)
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("reporting MySQL table %d: %w", count+1, err)
		}
		count++
	}
	if count != 6 {
		return fmt.Errorf("reporting MySQL migration has %d tables, expected 6", count)
	}
	return nil
}
