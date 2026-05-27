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
