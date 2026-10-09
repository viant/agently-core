package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var checkMigrationMu sync.Mutex

type checkTable struct {
	name, ddl    string
	withoutRowID bool
}

type checkSchemaObject struct {
	kind, name, table, ddl string
}

// PRAGMAs and transaction statements must use the same physical connection.
// BEGIN IMMEDIATE also serializes schema discovery with other file writers.
func removeSQLiteChecks(ctx context.Context, db *sql.DB) (resultErr error) {
	checkMigrationMu.Lock()
	defer checkMigrationMu.Unlock()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	transaction := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if transaction {
			_, err := conn.ExecContext(cleanup, "ROLLBACK")
			resultErr = errors.Join(resultErr, err)
			if err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
				return
			}
		}
		if _, err := conn.ExecContext(cleanup, fmt.Sprintf("PRAGMA foreign_keys=%d", foreignKeys)); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("restore SQLite foreign keys: %w", err))
			// Never return a connection with disabled FK enforcement to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin SQLite CHECK migration: %w", err)
	}
	transaction = true
	tables, err := sqliteCheckTables(ctx, conn)
	if err != nil {
		return err
	}
	if len(tables) > 0 {
		objects, err := sqliteCheckObjects(ctx, conn)
		if err != nil {
			return err
		}
		// Other tables' triggers and views can refer to a replaced table. Drop
		// them transactionally so ALTER RENAME never sees dangling references.
		for _, object := range objects {
			if object.kind != "index" {
				if _, err := conn.ExecContext(ctx, "DROP "+strings.ToUpper(object.kind)+" "+quoteSQLiteIdentifier(object.name)); err != nil {
					return fmt.Errorf("temporarily remove %s %s: %w", object.kind, object.name, err)
				}
			}
		}
		rebuilt := make(map[string]bool)
		for _, table := range tables {
			if err := rebuildSQLiteCheckTable(ctx, conn, table); err != nil {
				return fmt.Errorf("remove CHECKs from %s: %w", table.name, err)
			}
			rebuilt[table.name] = true
		}
		for _, object := range objects {
			if object.kind == "index" && !rebuilt[object.table] {
				continue
			}
			if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("restore %s %s: %w", object.kind, object.name, err)
			}
		}
		remaining, err := sqliteCheckTables(ctx, conn)
		if err != nil {
			return err
		}
		if len(remaining) != 0 {
			return fmt.Errorf("SQLite CHECK migration left %d constrained tables", len(remaining))
		}
		rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return err
		}
		violations := rows.Next()
		scanErr := rows.Err()
		rows.Close()
		if scanErr != nil {
			return scanErr
		}
		if violations {
			return fmt.Errorf("SQLite CHECK migration found foreign key violations")
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	transaction = false
	return nil
}

func sqliteCheckTables(ctx context.Context, conn *sql.Conn) ([]checkTable, error) {
	rows, err := conn.QueryContext(ctx, `SELECT s.name, s.sql, p.wr
FROM main.sqlite_schema s JOIN pragma_table_list p ON p.schema='main' AND p.name=s.name
WHERE p.type='table' AND s.type='table' AND substr(s.name,1,7)<>'sqlite_' AND s.sql IS NOT NULL
ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []checkTable
	for rows.Next() {
		var table checkTable
		if err := rows.Scan(&table.name, &table.ddl, &table.withoutRowID); err != nil {
			return nil, err
		}
		ddl, count, err := stripSQLiteChecks(table.ddl)
		if err != nil {
			return nil, fmt.Errorf("inspect CHECKs in %s: %w", table.name, err)
		}
		if count > 0 {
			table.ddl = ddl
			result = append(result, table)
		}
	}
	return result, rows.Err()
}

func sqliteCheckObjects(ctx context.Context, conn *sql.Conn) ([]checkSchemaObject, error) {
	rows, err := conn.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM main.sqlite_schema
WHERE type IN ('index','view','trigger') AND sql IS NOT NULL
ORDER BY CASE type WHEN 'index' THEN 0 WHEN 'view' THEN 1 ELSE 2 END, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []checkSchemaObject
	for rows.Next() {
		var object checkSchemaObject
		if err := rows.Scan(&object.kind, &object.name, &object.table, &object.ddl); err != nil {
			return nil, err
		}
		result = append(result, object)
	}
	return result, rows.Err()
}

func rebuildSQLiteCheckTable(ctx context.Context, conn *sql.Conn, table checkTable) error {
	var replacement string
	for i := 0; ; i++ {
		replacement = fmt.Sprintf("__agently_no_checks_%d", i)
		var exists int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM main.sqlite_schema WHERE name=?", replacement).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			break
		}
	}
	ddl, err := sqliteReplacementDDL(table.ddl, replacement)
	if err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA main.table_xinfo("+quoteSQLiteIdentifier(table.name)+")")
	if err != nil {
		return err
	}
	var columns []string
	names := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk, hidden int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk, &hidden); err != nil {
			rows.Close()
			return err
		}
		names[strings.ToLower(name)] = true
		if hidden == 0 {
			columns = append(columns, quoteSQLiteIdentifier(name))
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !table.withoutRowID {
		alias := ""
		for _, name := range []string{"rowid", "_rowid_", "oid"} {
			if !names[name] {
				alias = name
				break
			}
		}
		if alias == "" {
			return fmt.Errorf("all rowid aliases are shadowed; cannot safely preserve rowids")
		}
		columns = append(columns, quoteSQLiteIdentifier(alias))
	}
	var sequence sql.NullInt64
	var hasSequence int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM main.sqlite_schema WHERE name='sqlite_sequence'").Scan(&hasSequence); err != nil {
		return err
	}
	if hasSequence != 0 {
		if err := conn.QueryRowContext(ctx, "SELECT seq FROM main.sqlite_sequence WHERE name=?", table.name).Scan(&sequence); err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, ddl); err != nil {
		return err
	}
	columnList := strings.Join(columns, ",")
	if _, err := conn.ExecContext(ctx, "INSERT INTO "+quoteSQLiteIdentifier(replacement)+" ("+columnList+") SELECT "+columnList+" FROM "+quoteSQLiteIdentifier(table.name)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE "+quoteSQLiteIdentifier(table.name)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "ALTER TABLE "+quoteSQLiteIdentifier(replacement)+" RENAME TO "+quoteSQLiteIdentifier(table.name)); err != nil {
		return err
	}
	if sequence.Valid {
		if _, err := conn.ExecContext(ctx, "DELETE FROM main.sqlite_sequence WHERE name IN (?,?)", table.name, replacement); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO main.sqlite_sequence(name,seq) VALUES(?,?)", table.name, sequence.Int64); err != nil {
			return err
		}
	}
	return nil
}
