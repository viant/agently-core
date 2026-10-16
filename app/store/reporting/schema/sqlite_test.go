package schema

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteReportingBaselineIsIdempotentAndRetainsTableNames(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "reporting.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON; CREATE TABLE conversation(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := UpSQLite(ctx, db); err != nil {
			t.Fatalf("migration pass %d: %v", i+1, err)
		}
	}
	for _, name := range []string{"report_shared_artifact", "report_export_job", "report_export_artifact", "report_audit_event", "report_run", "conversation_report_context"} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("report table %q count=%d err=%v", name, count, err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES('c1')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id) VALUES('run-1','alice','c1','test','running','2026-01-01',1,'request-1')"); err != nil {
		t.Fatalf("existing report-run contract rejected: %v", err)
	}
}
