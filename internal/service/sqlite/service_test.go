package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

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
