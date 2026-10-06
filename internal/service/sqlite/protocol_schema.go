package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	iscript "github.com/viant/agently-core/internal/script"
	"strings"
)

// Upgrade only protocol columns, reading their definitions from the canonical
// bootstrap DDL. Existing application rows and execution defaults are retained.
// This is schema provisioning, not an application data-access path.
func ensureProtocolReuseColumns(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"conversation", "run", "call_payload"} {
		exists, err := sqliteTableExists(ctx, db, table)
		if err != nil {
			return fmt.Errorf("inspect protocol schema %s: %w", table, err)
		}
		if !exists {
			continue
		}
		start := strings.Index(iscript.SqlListScript, "CREATE TABLE IF NOT EXISTS "+table+" (")
		if start < 0 {
			return fmt.Errorf("protocol schema table %s is not declared", table)
		}
		body := iscript.SqlListScript[start:]
		end := strings.Index(body, "\n);")
		if end < 0 {
			return fmt.Errorf("protocol schema table %s is incomplete", table)
		}
		for _, line := range strings.Split(body[:end], "\n")[1:] {
			definition := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
			fields := strings.Fields(definition)
			if len(fields) < 2 {
				continue
			}
			column := fields[0]
			selected := strings.HasPrefix(column, "protocol_") || table == "run" && column == "run_kind" || table == "call_payload" && (column == "run_id" || column == "sequence")
			if !selected {
				continue
			}
			present, err := sqliteColumnExists(ctx, db, table, column)
			if err != nil {
				return fmt.Errorf("inspect protocol column %s.%s: %w", table, column, err)
			}
			if present {
				continue
			}
			statement := "ALTER TABLE " + table + " ADD COLUMN " + definition
			if _, err := db.ExecContext(ctx, statement); err != nil && !isDuplicateSQLiteColumnError(statement, err) {
				return fmt.Errorf("upgrade protocol column %s.%s: %w", table, column, err)
			}
		}
	}
	return nil
}

// These four tables belong only to the unreleased protocol POC. The user has
// explicitly authorized discarding their sandbox contents; original application
// tables and records are never removed here.
func dropRetiredProtocolPOCTables(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"agui_lease", "agui_event", "agui_run", "agui_thread"} {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			return fmt.Errorf("remove retired protocol POC table %s: %w", table, err)
		}
	}
	return tx.Commit()
}
