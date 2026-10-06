package agui

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	moderncsqlite "modernc.org/sqlite"
	sqlitecode "modernc.org/sqlite/lib"
)

type contentionCode int

func (c contentionCode) Error() string { return "database is locked" }
func (c contentionCode) Code() int     { return int(c) }

func TestTransactionContentionSQLite(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "lock.db")
	first, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer first.Close()
	second, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer second.Close()
	_, err = first.Exec("CREATE TABLE contention (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	tx, err := first.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.Exec("INSERT INTO contention VALUES (1)")
	require.NoError(t, err)
	_, err = second.Exec("INSERT INTO contention VALUES (2)")
	require.Error(t, err)
	var coded *moderncsqlite.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, sqlitecode.SQLITE_BUSY, coded.Code())
	require.True(t, transactionContention(err))
	require.True(t, transactionContention(fmt.Errorf("write failed: %w", err)))
	rows, err := tx.Query("SELECT * FROM contention")
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	_, err = tx.Exec("DROP TABLE contention")
	require.Error(t, err)
	require.ErrorAs(t, err, &coded)
	require.Equal(t, sqlitecode.SQLITE_LOCKED, coded.Code()&0xff)
	require.True(t, transactionContention(fmt.Errorf("drop failed: %w", err)))
}

func TestTransactionContentionRejectsUncodedErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New("database is locked"), errors.New("SQLITE_BUSY (5)"), contentionCode(sqlitecode.SQLITE_BUSY)} {
		require.False(t, transactionContention(err))
	}
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec("SELECT * FROM missing_table")
	require.Error(t, err)
	require.False(t, transactionContention(fmt.Errorf("query: %w", err)))
}

func TestTransactionContentionSQLiteBusySnapshot(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "snapshot.db")
	first, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer first.Close()
	second, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	defer second.Close()
	_, err = first.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	_, err = first.Exec("CREATE TABLE contention (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	tx, err := first.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	var count int
	require.NoError(t, tx.QueryRow("SELECT count(*) FROM contention").Scan(&count))
	_, err = second.Exec("INSERT INTO contention VALUES (1)")
	require.NoError(t, err)
	_, err = tx.Exec("INSERT INTO contention VALUES (2)")
	require.Error(t, err)
	var coded *moderncsqlite.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, sqlitecode.SQLITE_BUSY_SNAPSHOT, coded.Code())
	require.True(t, transactionContention(fmt.Errorf("write snapshot: %w", err)))
}

func TestTransactionContentionMySQL(t *testing.T) {
	for _, number := range []uint16{1205, 1213, 1062} {
		err := fmt.Errorf("transaction: %w", &mysql.MySQLError{Number: number})
		require.Equal(t, number != 1062, transactionContention(err))
	}
}
