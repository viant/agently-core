package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"path/filepath"
	"testing"
)

//go:embed testdata/pre_protocol_schema.sql
var preProtocolSchema string

func TestProtocolReuseUpgradePreservesNativeDataAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(preProtocolSchema); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO conversation(id,created_by_user_id,title,metadata) VALUES('c','u','Keep original title','{"custom":true}')`,
		`INSERT INTO turn(id,conversation_id,status) VALUES('t','c','completed')`,
		`INSERT INTO run(id,conversation_id,turn_id,status,checkpoint_data,lease_owner) VALUES('r','c','t','completed','{"checkpoint":7}','execution-owner')`,
		`INSERT INTO message(id,conversation_id,turn_id,role,content) VALUES('m','c','t','user','Original question')`,
		`INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body) VALUES('p','tool_response','application/json',2,'inline','{}')`,
		`INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id,report_fill_json) VALUES('report','u','c','forge','completed',CURRENT_TIMESTAMP,2,'ui-request','{"data":"kept"}')`,
		// Disposable POC protocol tables are not part of the old application schema.
		`CREATE TABLE agui_thread(thread_id TEXT PRIMARY KEY)`,
		`CREATE TABLE agui_run(run_key TEXT PRIMARY KEY,thread_id TEXT REFERENCES agui_thread(thread_id))`,
		`CREATE TABLE agui_event(event_key TEXT PRIMARY KEY,run_key TEXT REFERENCES agui_run(run_key))`,
		`CREATE TABLE agui_lease(run_key TEXT PRIMARY KEY REFERENCES agui_run(run_key))`,
		`INSERT INTO agui_thread VALUES('discard-poc')`,
	}
	for _, stmt := range statements {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	service := New("").WithPath(path)
	for i := 0; i < 2; i++ {
		if _, err = service.Ensure(ctx); err != nil {
			t.Fatalf("upgrade %d: %v", i, err)
		}
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assertNoSQLiteChecks(t, db)
	var kind, title, metadata, checkpoint, lease, question, fill string
	if err = db.QueryRow(`SELECT run_kind,checkpoint_data,lease_owner FROM run WHERE id='r'`).Scan(&kind, &checkpoint, &lease); err != nil {
		t.Fatal(err)
	}
	if kind != "execution" || checkpoint != `{"checkpoint":7}` || lease != "execution-owner" {
		t.Fatalf("native run changed: %q %q %q", kind, checkpoint, lease)
	}
	if err = db.QueryRow(`SELECT title,metadata FROM conversation WHERE id='c'`).Scan(&title, &metadata); err != nil {
		t.Fatal(err)
	}
	if title != "Keep original title" || metadata != `{"custom":true}` {
		t.Fatal("conversation changed")
	}
	if err = db.QueryRow(`SELECT content FROM message WHERE id='m'`).Scan(&question); err != nil || question != "Original question" {
		t.Fatalf("message changed: %v", err)
	}
	if err = db.QueryRow(`SELECT report_fill_json FROM report_run WHERE report_run_id='report'`).Scan(&fill); err != nil || fill != `{"data":"kept"}` {
		t.Fatalf("report changed: %v", err)
	}
	for _, table := range []string{"agui_thread", "agui_run", "agui_event", "agui_lease"} {
		exists, err := sqliteTableExists(ctx, db, table)
		if err != nil || exists {
			t.Fatalf("retired %s still present: %v", table, err)
		}
	}
	for _, table := range []string{"conversation", "run", "call_payload"} {
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s original records lost: count=%d err=%v", table, count, err)
		}
	}
	var violations int
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		violations++
	}
	if violations != 0 {
		t.Fatalf("foreign key violations: %d", violations)
	}
}
