package schema

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestMySQLReportingBaselineMatchesDeployedConstraints(t *testing.T) {
	dsn := os.Getenv("FORGE_REPORTING_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("FORGE_REPORTING_MYSQL_TEST_DSN requires an authorized local MySQL test server")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid local MySQL test DSN")
	}
	config.DBName = ""
	config.ParseTime = true
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("forge_reporting_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec("DROP DATABASE IF EXISTS `" + name + "`") }()
	config.DBName = name
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE conversation(id VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci PRIMARY KEY) ENGINE=InnoDB"); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if err := UpMySQL(ctx, db); err != nil {
			t.Fatalf("MySQL migration pass %d: %v", pass+1, err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name IN ('report_shared_artifact','report_export_job','report_export_artifact','report_audit_event','report_run','conversation_report_context')").Scan(&count); err != nil || count != 6 {
		t.Fatalf("reporting tables=%d err=%v", count, err)
	}
	var table, definition string
	if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE report_export_job").Scan(&table, &definition); err != nil || !strings.Contains(definition, "fk_report_export_job_run") || !strings.Contains(definition, "chk_report_export_job_run_reference") {
		t.Fatalf("job constraints table=%q err=%v", table, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO report_export_job(job_id,artifact_ref,owner_id,format,scope,status) VALUES('bad','ref','alice','pdf','draft','not-a-status')"); err == nil {
		t.Fatal("invalid job status accepted")
	}
}
