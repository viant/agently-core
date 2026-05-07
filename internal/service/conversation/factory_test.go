package conversation

import (
	"testing"

	"github.com/viant/datly/view"
)

func TestApplySQLitePoolDefaults_SetsSingleConnectionDefaults(t *testing.T) {
	conn := view.NewConnector("agently", "sqlite", "file:test.db")

	applySQLitePoolDefaults(conn)

	if conn.MaxOpenConns != defaultSQLiteMaxOpenConns {
		t.Fatalf("MaxOpenConns = %d, want %d", conn.MaxOpenConns, defaultSQLiteMaxOpenConns)
	}
	if conn.MaxIdleConns != defaultSQLiteMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", conn.MaxIdleConns, defaultSQLiteMaxIdleConns)
	}
}

func TestApplySQLitePoolDefaults_PreservesExplicitPoolConfig(t *testing.T) {
	conn := view.NewConnector("agently", "sqlite", "file:test.db")
	conn.MaxOpenConns = 3
	conn.MaxIdleConns = 2

	applySQLitePoolDefaults(conn)

	if conn.MaxOpenConns != 3 {
		t.Fatalf("MaxOpenConns = %d, want 3", conn.MaxOpenConns)
	}
	if conn.MaxIdleConns != 2 {
		t.Fatalf("MaxIdleConns = %d, want 2", conn.MaxIdleConns)
	}
}
