package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/viant/agently-core/internal/testutil/dbtest"
	_ "modernc.org/sqlite"
)

func TestService_EnsureInMemory(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='conversation'").Scan(&name); err != nil {
		t.Fatalf("conversation table missing: %v", err)
	}
	if name != "conversation" {
		t.Fatalf("unexpected table name: %s", name)
	}

	for _, table := range []string{
		"report_shared_artifact",
		"report_export_job",
		"report_export_artifact",
		"report_audit_event",
	} {
		var got string
		if err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&got); err != nil {
			t.Fatalf("%s table missing: %v", table, err)
		}
		if got != table {
			t.Fatalf("unexpected table name for %s: %s", table, got)
		}
	}
}

func TestService_EnsureInMemory_MessageLookupIndexesExist(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT name
		FROM pragma_index_list('message')
		ORDER BY name
	`)
	if err != nil {
		t.Fatalf("pragma_index_list(message): %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan index name: %v", err)
		}
		names = append(names, name)
	}

	for _, want := range []string{
		"idx_message_parent",
		"idx_message_attachment_payload",
		"idx_message_elicitation_payload",
		"idx_message_parent_seq_created",
		"idx_message_parent_attachment",
		"idx_message_parent_elicitation",
	} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing message index %q, have %s", want, strings.Join(names, ", "))
		}
	}
}

func TestService_EnsureInMemory_MessageRelationIndexesExist(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	expected := map[string][]string{
		"message": {"idx_message_superseded_by", "idx_message_linked_conversation"},
	}

	for table, indexes := range expected {
		for _, index := range indexes {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, table, index).Scan(&count); err != nil {
				t.Fatalf("query index %s on %s: %v", index, table, err)
			}
			if count != 1 {
				t.Errorf("missing index %s on %s", index, table)
			}
		}
	}
}

func TestService_EnsureInMemory_MessageAttachmentLookupUsesIndex(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT inline_body, compression, uri, mime_type, m.parent_message_id
		FROM message m
		JOIN call_payload p ON m.attachment_payload_id = p.id
		WHERE m.attachment_payload_id IS NOT NULL
		  AND m.parent_message_id IN ('a', 'b', 'c')
	`)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var (
			id, parent, notused int
			detail              string
		)
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan query plan row: %v", err)
		}
		details = append(details, detail)
	}
	joined := strings.Join(details, " | ")
	if !strings.Contains(joined, "idx_message_parent_attachment") && !strings.Contains(joined, "idx_message_parent") && !strings.Contains(joined, "idx_message_attachment_payload") {
		t.Fatalf("expected indexed message lookup in plan, got %s", joined)
	}
	if strings.Contains(joined, "SCAN m") {
		t.Fatalf("unexpected full message scan in plan: %s", joined)
	}
}

func TestService_EnsureInMemory_MessageAttachmentLookupUsesCoveringIndex(t *testing.T) {
	svc := New("")
	dsn, err := svc.EnsureInMemory(context.Background())
	if err != nil {
		t.Fatalf("EnsureInMemory() error = %v", err)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		EXPLAIN QUERY PLAN
		SELECT inline_body, compression, uri, mime_type, m.parent_message_id
		FROM message m
		JOIN call_payload p ON m.attachment_payload_id = p.id
		WHERE m.attachment_payload_id IS NOT NULL
		  AND m.parent_message_id IN ('a', 'b', 'c')
	`)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()

	var details []string
	for rows.Next() {
		var (
			id, parent, notused int
			detail              string
		)
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan query plan row: %v", err)
		}
		details = append(details, detail)
	}
	joined := strings.Join(details, " | ")
	if !strings.Contains(joined, "idx_message_parent_attachment") {
		t.Fatalf("expected parent attachment index in plan, got %s", joined)
	}
}

func TestService_Ensure_UpgradesLegacyToolApprovalQueueSchema(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "legacy-tool-approval-queue")
	defer cleanup()

	_, err := db.Exec(`
		CREATE TABLE tool_approval_queue (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			conversation_id TEXT,
			turn_id TEXT,
			message_id TEXT,
			tool_name TEXT NOT NULL,
			title TEXT,
			arguments BLOB NOT NULL,
			metadata BLOB,
			status TEXT NOT NULL DEFAULT 'pending',
			decision TEXT,
			approved_by_user_id TEXT,
			approved_at DATETIME,
			executed_at DATETIME,
			error_message TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create legacy tool_approval_queue: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	svc := New(filepath.Dir(filepath.Dir(dbPath))).WithPath(dbPath)
	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() should upgrade legacy tool_approval_queue schema: %v", err)
	}

	upgraded, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer upgraded.Close()

	for _, column := range []string{"expires_at", "timed_out_at"} {
		var found bool
		rows, err := upgraded.Query(`PRAGMA table_info(tool_approval_queue)`)
		if err != nil {
			t.Fatalf("pragma table_info(tool_approval_queue): %v", err)
		}
		for rows.Next() {
			var (
				cid        int
				name       string
				dataType   string
				notNull    int
				defaultVal sql.NullString
				primaryKey int
			)
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &primaryKey); err != nil {
				rows.Close()
				t.Fatalf("scan table_info row: %v", err)
			}
			if name == column {
				found = true
			}
		}
		rows.Close()
		if !found {
			t.Fatalf("expected upgraded tool_approval_queue to contain column %q", column)
		}
	}

	var indexSQL string
	if err := upgraded.QueryRow(`
		SELECT sql
		FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_taq_status_expires_at'
	`).Scan(&indexSQL); err != nil {
		t.Fatalf("expected idx_taq_status_expires_at to exist: %v", err)
	}
	if !strings.Contains(indexSQL, "expires_at") {
		t.Fatalf("expected idx_taq_status_expires_at to reference expires_at, got %q", indexSQL)
	}
}

func TestService_Ensure_UpgradesLegacyGoalSchema(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "legacy-goal")
	defer cleanup()

	_, err := db.Exec(`
		CREATE TABLE goal (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			objective TEXT NOT NULL,
			status TEXT NOT NULL,
			pause_reason TEXT,
			controller_spec TEXT,
			token_budget INTEGER,
			tokens_used INTEGER NOT NULL DEFAULT 0,
			time_used_seconds INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create legacy goal: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	svc := New(filepath.Dir(filepath.Dir(dbPath))).WithPath(dbPath)
	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() should upgrade legacy goal schema: %v", err)
	}

	upgraded, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer upgraded.Close()

	ok, err := sqliteColumnExists(context.Background(), upgraded, "goal", "status_reason")
	if err != nil {
		t.Fatalf("sqliteColumnExists(goal.status_reason) error = %v", err)
	}
	if !ok {
		t.Fatalf("expected goal.status_reason column to exist after Ensure()")
	}
	for _, column := range []string{"autonomous_turns_used", "consecutive_no_progress", "last_continuation_fingerprint"} {
		ok, err = sqliteColumnExists(context.Background(), upgraded, "goal", column)
		if err != nil {
			t.Fatalf("sqliteColumnExists(goal.%s) error = %v", column, err)
		}
		if !ok {
			t.Fatalf("expected goal.%s column to exist after Ensure()", column)
		}
	}
}

func TestService_Ensure_UpgradesLegacyTurnSchema(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "legacy-turn")
	defer cleanup()

	_, err := db.Exec(`
		CREATE TABLE turn (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			queue_seq INTEGER,
			status TEXT NOT NULL,
			error_message TEXT,
			started_by_message_id TEXT,
			retry_of TEXT,
			agent_id_used TEXT,
			agent_config_used_id TEXT,
			model_override_provider TEXT,
			model_override TEXT,
			model_params_override TEXT,
			run_id TEXT
		)
	`)
	if err != nil {
		t.Fatalf("create legacy turn: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	svc := New(filepath.Dir(filepath.Dir(dbPath))).WithPath(dbPath)
	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() should upgrade legacy turn schema: %v", err)
	}

	upgraded, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer upgraded.Close()

	for _, column := range []string{"origin", "goal_id", "status_reason"} {
		ok, err := sqliteColumnExists(context.Background(), upgraded, "turn", column)
		if err != nil {
			t.Fatalf("sqliteColumnExists(turn.%s) error = %v", column, err)
		}
		if !ok {
			t.Fatalf("expected turn.%s column to exist after Ensure()", column)
		}
	}
}

func TestService_Ensure_UpgradesLegacyScheduleSchema(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "legacy-schedule")
	defer cleanup()

	_, err := db.Exec(`
		CREATE TABLE schedule (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			created_by_user_id TEXT,
			visibility TEXT NOT NULL DEFAULT 'private',
			agent_ref TEXT NOT NULL,
			model_override TEXT,
			user_cred_url TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			start_at DATETIME,
			end_at DATETIME,
			schedule_type TEXT NOT NULL DEFAULT 'cron',
			cron_expr TEXT,
			interval_seconds INTEGER,
			timezone TEXT NOT NULL DEFAULT 'UTC',
			timeout_seconds INTEGER NOT NULL DEFAULT 0,
			task_prompt_uri TEXT,
			task_prompt TEXT,
			next_run_at DATETIME,
			last_run_at DATETIME,
			last_status TEXT,
			last_error TEXT,
			lease_owner TEXT,
			lease_until DATETIME,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create legacy schedule: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	svc := New(filepath.Dir(filepath.Dir(dbPath))).WithPath(dbPath)
	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() should upgrade legacy schedule schema: %v", err)
	}

	upgraded, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer upgraded.Close()

	for _, column := range []string{"internal", "conversation_id", "goal_id"} {
		ok, err := sqliteColumnExists(context.Background(), upgraded, "schedule", column)
		if err != nil {
			t.Fatalf("sqliteColumnExists(schedule.%s) error = %v", column, err)
		}
		if !ok {
			t.Fatalf("expected schedule.%s column to exist after Ensure()", column)
		}
	}
}

func TestService_Ensure_UpgradesLegacyReportExportJobSchema(t *testing.T) {
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "legacy-report-export-job")
	defer cleanup()

	_, err := db.Exec(`
		CREATE TABLE report_export_job (
			job_id TEXT PRIMARY KEY,
			artifact_ref TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			conversation_id TEXT,
			workspace_id TEXT,
			auth_context_ref TEXT,
			format TEXT NOT NULL,
			scope TEXT NOT NULL,
			status TEXT NOT NULL,
			report_spec_json BLOB,
			report_fill_json BLOB,
			report_print_json BLOB,
			metadata_json BLOB,
			artifact_id TEXT,
			error_text TEXT,
			diagnostics_json BLOB,
			submitted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME,
			retention_ttl_sec INTEGER NOT NULL DEFAULT 0
		)
	`)
	if err != nil {
		t.Fatalf("create legacy report_export_job: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO report_export_job (
			job_id, artifact_ref, owner_id, conversation_id, format, scope, status, retention_ttl_sec
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, "job-legacy", "reports/monthly", "owner-1", "conversation-1", "pdf", "draft", "queued", 3600)
	if err != nil {
		t.Fatalf("insert legacy report_export_job: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	svc := New(filepath.Dir(filepath.Dir(dbPath))).WithPath(dbPath)
	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("Ensure() should upgrade legacy report_export_job schema: %v", err)
	}

	upgraded, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer upgraded.Close()

	wantTypes := map[string]string{
		"report_run_id":       "TEXT",
		"report_run_revision": "INTEGER",
		"export_request_id":   "TEXT",
	}
	gotTypes := map[string]string{}
	rows, err := upgraded.Query(`PRAGMA table_info(report_export_job)`)
	if err != nil {
		t.Fatalf("pragma table_info(report_export_job): %v", err)
	}
	for rows.Next() {
		var (
			cid        int
			name       string
			dataType   string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultVal, &primaryKey); err != nil {
			rows.Close()
			t.Fatalf("scan table_info row: %v", err)
		}
		if _, ok := wantTypes[name]; ok {
			gotTypes[name] = dataType
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close table_info rows: %v", err)
	}
	for column, wantType := range wantTypes {
		if gotType, ok := gotTypes[column]; !ok {
			t.Errorf("expected report_export_job.%s column to exist after Ensure()", column)
		} else if gotType != wantType {
			t.Errorf("report_export_job.%s type = %q, want %q", column, gotType, wantType)
		}
	}

	var (
		artifactRef    string
		ownerID        string
		conversationID string
		format         string
		scope          string
		status         string
		retentionTTL   int
	)
	if err := upgraded.QueryRow(`
		SELECT artifact_ref, owner_id, conversation_id, format, scope, status, retention_ttl_sec
		FROM report_export_job
		WHERE job_id = ?
	`, "job-legacy").Scan(&artifactRef, &ownerID, &conversationID, &format, &scope, &status, &retentionTTL); err != nil {
		t.Fatalf("query preserved legacy report_export_job: %v", err)
	}
	if artifactRef != "reports/monthly" || ownerID != "owner-1" || conversationID != "conversation-1" ||
		format != "pdf" || scope != "draft" || status != "queued" || retentionTTL != 3600 {
		t.Fatalf("legacy report_export_job data was not preserved")
	}

	if _, err := svc.Ensure(context.Background()); err != nil {
		t.Fatalf("second Ensure() should be repeat-safe: %v", err)
	}
}
