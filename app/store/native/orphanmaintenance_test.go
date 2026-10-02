package native_test

import (
	"context"
	"database/sql"
	"fmt"
	msgwrite "github.com/viant/agently-core/internal/datly/message/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	orphan "github.com/viant/agently-core/internal/store/orphanmaintenance"
)

func orphanFixture(t *testing.T) (*orphan.Store, *maintenance.Store, *sql.DB) {
	t.Helper()
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	_, file, _, _ := runtime.Caller(0)
	root := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(root, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return &orphan.Store{Invoker: server}, &maintenance.Store{Invoker: server}, db
}
func seedOrphanFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	statements := []string{
		"INSERT INTO conversation(id,created_at,updated_at,status,created_by_user_id) VALUES('conversation-old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','succeeded','historical-owner')",
		"INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,compression,created_at) VALUES('payload-old','fixture','text/plain',0,'inline','none','2026-01-01T00:00:00Z')",
		"INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,compression,created_at) VALUES('payload-recent','fixture','text/plain',0,'inline','none','2026-01-03T00:00:00Z')",
		"INSERT INTO message(id,conversation_id,created_at,updated_at,role,type,linked_conversation_id,content) VALUES('message-old','conversation-old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','assistant','text','missing-conversation','unchanged content')",
		"INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state,created_at,updated_at) VALUES('claim-old','rule','tool','missing-turn','hash','failed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"INSERT INTO schedule(id,name,conversation_id,agent_ref,schedule_type,timezone,created_at,updated_at) VALUES('schedule-old','schedule-old','missing-schedule-conversation','agent','adhoc','UTC','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"INSERT INTO report_audit_event(event_id,event_type,artifact_ref,version,job_id,actor_id,occurred_at) VALUES('audit-old','fixture','artifact',1,'missing-job','owner','2026-01-01T00:00:00Z')",
		"INSERT INTO report_shared_artifact(artifact_id,artifact_ref,owner_id,kind,lifecycle,source_artifact_id,created_at,updated_at) VALUES('shared-old','artifact','owner','report','saved','missing-source','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"PRAGMA foreign_keys=OFF",
		"INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,updated_at) VALUES('owner','conversation-old','missing-report',1,'2026-01-01T00:00:00Z')",
	}
	for _, statement := range statements {
		_, err := db.Exec(statement)
		require.NoError(t, err)
	}
}
func orphanLease(t *testing.T, store *maintenance.Store, key, owner string) maintenance.Lease {
	t.Helper()
	result, err := store.Acquire(context.Background(), key, owner, time.Minute)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	return result.Lease
}
func TestWorkspaceRuntimeOrphanReportAndFencedMutations(t *testing.T) {
	store, leases, db := orphanFixture(t)
	seedOrphanFixture(t, db)
	ctx := context.Background()
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	rows, err := store.List(ctx, orphan.CandidateRequest{OlderThan: cutoff, Limit: 100})
	require.NoError(t, err)
	require.Len(t, rows, 5)
	pageIDs := []string{}
	cursor := ""
	for {
		page, err := store.List(ctx, orphan.CandidateRequest{OlderThan: cutoff, AfterCursor: cursor, Limit: 1})
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		require.Len(t, page, 1)
		pageIDs = append(pageIDs, page[0].RuleID+"\x00"+page[0].RecordID)
		cursor = page[0].CursorID
	}
	expected := []string{}
	for _, row := range rows {
		expected = append(expected, row.RuleID+"\x00"+row.RecordID)
		require.NotEqual(t, "report_audit_event", row.Table)
		require.NotEqual(t, "report_shared_artifact", row.Table)
	}
	require.Equal(t, expected, pageIDs)
	_, err = db.Exec("INSERT INTO conversation(id,created_at,status) VALUES('missing-conversation','2026-01-01T00:00:00Z','succeeded')")
	require.NoError(t, err)
	lease := orphanLease(t, leases, "orphan-job", "worker")
	counts := map[orphan.Reason]int{}
	for _, row := range rows {
		result, err := store.Apply(ctx, orphan.Request{RuleID: row.RuleID, RecordID: row.RecordID, OlderThan: cutoff, Lease: lease})
		require.NoError(t, err)
		counts[result.Reason]++
	}
	require.Equal(t, 3, counts[orphan.Deleted])
	require.Equal(t, 1, counts[orphan.Detached])
	require.Equal(t, 1, counts[orphan.NoLongerEligible])
	var ref sql.NullString
	var updated string
	require.NoError(t, db.QueryRow("SELECT conversation_id,CAST(updated_at AS CHAR) FROM schedule WHERE id='schedule-old'").Scan(&ref, &updated))
	require.False(t, ref.Valid)
	require.Equal(t, "2026-01-01T00:00:00Z", updated, "private detach must not refresh timestamp")
	result, err := store.Apply(ctx, orphan.Request{RuleID: "call_payload.unused", RecordID: "payload-old", OlderThan: cutoff, Lease: lease})
	require.NoError(t, err)
	require.Equal(t, orphan.NoLongerEligible, result.Reason)
	for _, table := range []string{"report_audit_event", "report_shared_artifact"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		require.Equal(t, 1, count)
	}
}
func TestWorkspaceRuntimeOrphanStaleLeaseAndRollback(t *testing.T) {
	store, leases, db := orphanFixture(t)
	seedOrphanFixture(t, db)
	ctx := context.Background()
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	stale := orphanLease(t, leases, "orphan-job", "old-worker")
	released, err := leases.Release(ctx, stale)
	require.NoError(t, err)
	require.True(t, released)
	held := orphanLease(t, leases, "orphan-job", "new-worker")
	_, err = store.Apply(ctx, orphan.Request{RuleID: "call_payload.unused", RecordID: "payload-old", OlderThan: cutoff, Lease: stale})
	require.ErrorIs(t, err, maintenance.ErrLeaseLost)
	var leaseUpdated string
	require.NoError(t, db.QueryRow("SELECT CAST(updated_at AS CHAR) FROM maintenance_lease WHERE lease_key='orphan-job'").Scan(&leaseUpdated))
	_, err = db.Exec("CREATE TRIGGER orphan_detach_failure BEFORE UPDATE OF linked_conversation_id ON message BEGIN SELECT RAISE(ABORT,'orphan detach failure'); END")
	require.NoError(t, err)
	_, err = store.Apply(ctx, orphan.Request{RuleID: "message.missing_linked_conversation", RecordID: "message-old", OlderThan: cutoff, Lease: held})
	require.ErrorContains(t, err, "orphan detach failure")
	var link string
	require.NoError(t, db.QueryRow("SELECT linked_conversation_id FROM message WHERE id='message-old'").Scan(&link))
	require.Equal(t, "missing-conversation", link)
	var after string
	require.NoError(t, db.QueryRow("SELECT CAST(updated_at AS CHAR) FROM maintenance_lease WHERE lease_key='orphan-job'").Scan(&after))
	require.Equal(t, leaseUpdated, after, "lease fence update must roll back with orphan mutation")
}

func TestWorkspaceRuntimeOrphanDetachIsDedicatedAndStrict(t *testing.T) {
	store, leases, db := orphanFixture(t)
	ctx := context.Background()
	old := "2026-01-01T00:00:00Z"
	_, err := db.Exec("INSERT INTO conversation(id,created_at) VALUES('c',?)", old)
	require.NoError(t, err)
	for _, id := range []string{"ordinary", "private", "reject"} {
		_, err = db.Exec("INSERT INTO message(id,conversation_id,linked_conversation_id,role,type,content,created_at,updated_at) VALUES(?,'c','missing','assistant','text','unchanged',?,?)", id, old, old)
		require.NoError(t, err)
	}
	ordinary := &msgwrite.Message{}
	ordinary.SetId("ordinary")
	ordinary.SetLinkedConversationId(nil)
	input := &msgwrite.Input{}
	input.SetMessages([]*msgwrite.Message{ordinary})
	_, err = store.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[msgwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: input})
	require.NoError(t, err)
	var ordinaryUpdated string
	require.NoError(t, db.QueryRow("SELECT CAST(updated_at AS CHAR) FROM message WHERE id='ordinary'").Scan(&ordinaryUpdated))
	require.NotEqual(t, old, ordinaryUpdated, "ordinary lifecycle must retain its timestamp behavior")
	lease := orphanLease(t, leases, "detach-mode", "worker")
	result, err := store.Apply(ctx, orphan.Request{RuleID: "message.missing_linked_conversation", RecordID: "private", OlderThan: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), Lease: lease})
	require.NoError(t, err)
	require.True(t, result.Detached)
	var updated, content string
	var link sql.NullString
	var sequence sql.NullInt64
	require.NoError(t, db.QueryRow("SELECT CAST(updated_at AS CHAR),content,linked_conversation_id,sequence FROM message WHERE id='private'").Scan(&updated, &content, &link, &sequence))
	require.Equal(t, old, updated)
	require.Equal(t, "unchanged", content)
	require.False(t, link.Valid)
	require.False(t, sequence.Valid)
	rejected := &msgwrite.Message{}
	rejected.SetId("reject")
	replacement := "replacement"
	rejected.SetLinkedConversationId(&replacement)
	input = &msgwrite.Input{}
	input.SetMessages([]*msgwrite.Message{rejected})
	_, err = store.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[msgwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: input, Providers: []locator.Provider{provider.Named("orphandetach", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "enabledDetach":
			return true, true, nil
		case "column":
			return "linked_conversation_id", true, nil
		}
		return nil, false, nil
	})}})
	require.ErrorContains(t, err, "nil reference")
	rejected = &msgwrite.Message{}
	rejected.SetId("reject")
	rejected.SetLinkedConversationId(nil)
	status := "failed"
	rejected.SetStatus(&status)
	input = &msgwrite.Input{}
	input.SetMessages([]*msgwrite.Message{rejected})
	_, err = store.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[msgwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/api/agently/message"}}, Input: input, Providers: []locator.Provider{provider.Named("orphandetach", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "enabledDetach":
			return true, true, nil
		case "column":
			return "linked_conversation_id", true, nil
		}
		return nil, false, nil
	})}})
	require.ErrorContains(t, err, "one nil reference")
	handler, ok := store.Invoker.(http.Handler)
	require.True(t, ok)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/v1/api/agently/message?orphanDetach=true&orphanColumn=linked_conversation_id", strings.NewReader(`{"data":[{"id":"reject","linkedConversationId":null}],"orphanDetach":true,"orphanColumn":"linked_conversation_id"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	require.GreaterOrEqual(t, response.Code, 400, "public input must not enable private maintenance")
	var rejectedLink string
	require.NoError(t, db.QueryRow("SELECT linked_conversation_id FROM message WHERE id='reject'").Scan(&rejectedLink))
	require.Equal(t, "missing", rejectedLink)

}

func TestWorkspaceRuntimeOrphanEverySQLiteRule(t *testing.T) {
	store, leases, db := orphanFixture(t)
	runEveryOrphanRule(t, store, leases, db, false, "")
}
func runEveryOrphanRule(t *testing.T, store *orphan.Store, leases *maintenance.Store, db *sql.DB, mysql bool, prefix string) {
	t.Helper()
	owner := prefix + "historical-owner"
	transform := func(statement string) string {
		for _, key := range []string{"base-c", "base-t", "base-p", "base-m", "base-g", "base-s", "base-r", "base-rr", "base-j", "request", "base-export", "historical-owner", "missing"} {
			statement = strings.ReplaceAll(statement, "'"+key+"'", "'"+prefix+key+"'")
		}
		if mysql {
			statement = strings.ReplaceAll(statement, "2026-01-01T00:00:00Z", "2026-01-01 00:00:00")
			statement = strings.ReplaceAll(statement, "'request-'||?", "CONCAT('request-',?)")
			statement = strings.ReplaceAll(statement, "'copy'", "'eager'")
			statement = strings.ReplaceAll(statement, "'function'", "'general'")
			if strings.HasPrefix(statement, "INSERT INTO tool_approval_queue") {
				statement = strings.ReplaceAll(statement, "'completed'", "'executed'")
			}
			if strings.HasPrefix(statement, "INSERT INTO call_payload") {
				statement = strings.ReplaceAll(statement, "'fixture'", "'attachment'")
				statement = strings.ReplaceAll(statement, "'inline'", "'object'")
			}
		}
		return statement
	}
	exec := func(statement string, args ...any) (sql.Result, error) { return db.Exec(transform(statement), args...) }
	ctx := context.Background()
	cutoff := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	base := []string{
		"PRAGMA foreign_keys=OFF",
		"INSERT INTO conversation(id,created_at,status,created_by_user_id) VALUES('base-c','2026-01-01T00:00:00Z','succeeded','historical-owner')",
		"INSERT INTO turn(id,conversation_id,created_at,status) VALUES('base-t','base-c','2026-01-01T00:00:00Z','succeeded')",
		"INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,compression,created_at) VALUES('base-p','fixture','text/plain',0,'inline','none','2026-01-01T00:00:00Z')",
		"INSERT INTO message(id,conversation_id,turn_id,role,type,status,attachment_payload_id,created_at,updated_at) VALUES('base-m','base-c','base-t','assistant','text','completed','base-p','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"INSERT INTO goal(id,conversation_id,objective,status,created_at) VALUES('base-g','base-c','goal','completed','2026-01-01T00:00:00Z')",
		"INSERT INTO schedule(id,name,created_by_user_id,agent_ref,schedule_type,timezone,created_at,updated_at) VALUES('base-s','schedule','historical-owner','agent','adhoc','UTC','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"INSERT INTO run(id,conversation_id,turn_id,schedule_id,conversation_kind,status,created_at,updated_at) VALUES('base-r','base-c','base-t','base-s','scheduled','succeeded','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')",
		"INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,ui_run_request_id,created_at,updated_at,revision) VALUES('base-rr','historical-owner','base-c','fixture','completed','2026-01-01T00:00:00Z','request','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',1)",
		"INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status,submitted_at,report_run_id,report_run_revision,export_request_id) VALUES('base-j','ref','historical-owner','base-c','pdf','draft','succeeded','2026-01-01T00:00:00Z','base-rr',1,'base-export')",
	}
	for _, statement := range base {
		if mysql && strings.HasPrefix(statement, "PRAGMA") {
			continue
		}
		_, err := exec(statement)
		require.NoError(t, err)
	}
	acquired, acquireErr := leases.Acquire(ctx, prefix+"all-orphans", prefix+"maintenance-worker", time.Hour)
	require.NoError(t, acquireErr)
	require.True(t, acquired.Acquired)
	lease := acquired.Lease
	for i, rule := range orphan.Rules(mysql) {
		t.Run(rule.ID, func(t *testing.T) {
			id := fmt.Sprintf("%sorphan-%02d", prefix, i)
			recordID := id
			keys := []any{id}
			predicate := rule.Keys[0] + "=?"
			if rule.Table == "conversation_report_context" {
				recordID = owner + orphan.RecordSeparator + id
				keys = []any{owner, id}
				predicate = "owner_id=? AND conversation_id=?"
			}
			var statement string
			switch rule.Table {
			case "conversation":
				statement = "INSERT INTO conversation(id,created_at,updated_at,status,created_by_user_id) VALUES(?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','succeeded','historical-owner')"
			case "goal":
				statement = "INSERT INTO goal(id,conversation_id,objective,status,created_at,updated_at) VALUES(?,'missing','orphan goal','completed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "turn":
				statement = "INSERT INTO turn(id,conversation_id,created_at,status,goal_id,started_by_message_id,retry_of,run_id) VALUES(?,'base-c','2026-01-01T00:00:00Z','succeeded','base-g','base-m','base-t','base-r')"
			case "turn_queue":
				_, err := exec("INSERT INTO turn(id,conversation_id,created_at,status) VALUES(?,'base-c','2026-01-01T00:00:00Z','succeeded')", id+"-t")
				require.NoError(t, err)
				_, err = exec("INSERT INTO message(id,conversation_id,turn_id,role,type,created_at) VALUES(?,'base-c',?,'assistant','text','2026-01-01T00:00:00Z')", id+"-m", id+"-t")
				require.NoError(t, err)
				statement = "INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status,created_at,updated_at) VALUES(?,'base-c',?,?,1,'queued','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "message":
				statement = "INSERT INTO message(id,conversation_id,turn_id,role,type,status,content,created_at,updated_at) VALUES(?,'base-c','base-t','assistant','text','completed','content unchanged','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "model_call", "tool_call":
				if rule.ID != rule.Table+".missing_message" {
					_, err := exec("INSERT INTO message(id,conversation_id,turn_id,role,type,status,created_at) VALUES(?,'base-c','base-t','assistant','text','completed','2026-01-01T00:00:00Z')", id)
					require.NoError(t, err)
				}
				if rule.Table == "model_call" {
					statement = "INSERT INTO model_call(message_id,turn_id,run_id,provider,model,model_kind,status,started_at,completed_at) VALUES(?,'base-t','base-r','openai','fixture','chat','completed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
				} else {
					statement = "INSERT INTO tool_call(message_id,turn_id,run_id,op_id,tool_name,tool_kind,status,started_at,completed_at) VALUES(?,'base-t','base-r',?,'tool','function','completed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
				}
			case "generated_file":
				statement = "INSERT INTO generated_file(id,conversation_id,turn_id,message_id,payload_id,provider,mode,copy_mode,mime_type,created_at,updated_at,status) VALUES(?,'base-c','base-t','base-m','base-p','fixture','inline','copy','text/plain','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','ready')"
			case "tool_execution_claim":
				statement = "INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state,created_at,updated_at) VALUES(?,'rule','tool','missing','hash','completed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "tool_approval_queue":
				statement = "INSERT INTO tool_approval_queue(id,user_id,conversation_id,turn_id,message_id,tool_name,arguments,status,created_at,updated_at) VALUES(?,'historical-owner','base-c','base-t','base-m','tool','{}','completed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "run":
				statement = "INSERT INTO run(id,conversation_id,turn_id,schedule_id,conversation_kind,status,resumed_from_run_id,checkpoint_message_id,created_at,updated_at) VALUES(?,'base-c','base-t','base-s','scheduled','succeeded','base-r','base-m','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "schedule":
				statement = "INSERT INTO schedule(id,name,created_by_user_id,conversation_id,goal_id,agent_ref,schedule_type,timezone,created_at,updated_at) VALUES(?,?,'historical-owner','base-c','base-g','agent','adhoc','UTC','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "report_run":
				statement = "INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,ui_run_request_id,created_at,updated_at,revision) VALUES(?,'historical-owner','base-c','fixture','completed','2026-01-01T00:00:00Z','request-'||?, '2026-01-01T00:00:00Z','2026-01-01T00:00:00Z',3)"
			case "report_export_job":
				statement = "INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,format,scope,status,submitted_at) VALUES(?,'ref','historical-owner','base-c','pdf','draft','succeeded','2026-01-01T00:00:00Z')"
			case "report_export_artifact":
				statement = "INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type,created_at,retention_ttl_sec) VALUES(?,'missing','ref','historical-owner','pdf','application/pdf','2026-01-01T00:00:00Z',0)"
			case "conversation_report_context":
				statement = "INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,updated_at) VALUES('historical-owner',?,'missing',1,'2026-01-01T00:00:00Z')"
			case "schedule_run":
				statement = "INSERT INTO schedule_run(id,schedule_id,conversation_id,status,conversation_kind,created_at,updated_at) VALUES(?,'base-s','base-c','failed','scheduled','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')"
			case "investigation":
				statement = "INSERT INTO investigation(id,title,created_by,conversation_id,created) VALUES(?,'orphan','historical-owner','missing','2026-01-01T00:00:00Z')"
			case "call_payload":
				statement = "INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,compression,created_at) VALUES(?,'fixture','text/plain',0,'inline','none','2026-01-01T00:00:00Z')"
			default:
				t.Fatalf("fixture table unsupported: %s", rule.Table)
			}
			args := []any{id}
			if rule.Table == "report_run" || rule.Table == "tool_call" || rule.Table == "schedule" {
				args = append(args, id)
			}
			if rule.Table == "turn_queue" {
				args = append(args, id+"-t", id+"-m")
			}
			_, err := exec(statement, args...)
			require.NoError(t, err)
			column := rule.DetachColumn
			if rule.Action == orphan.SafeDelete {
				switch rule.ID {
				case "turn.missing_conversation", "turn_queue.missing_conversation", "message.missing_conversation", "generated_file.missing_conversation":
					column = "conversation_id"
				case "schedule_run.missing_schedule":
					column = "schedule_id"
				case "turn_queue.missing_turn":
					column = "turn_id"
				case "turn_queue.missing_message":
					column = "message_id"
				case "conversation_report_context.missing_conversation":
					column = "conversation_id"
				}
			}
			if rule.Action == orphan.ReportOnly {
				if rule.ID == "report_export_job.missing_report_run" {
					column = "report_run_id"
				} else {
					column = "artifact_id"
				}
			}
			if rule.ID == "report_export_job.missing_report_run" {
				_, err = exec("UPDATE report_export_job SET report_run_id='missing',report_run_revision=1,export_request_id=? WHERE job_id=?", id, id)
				require.NoError(t, err)
				column = ""
			}
			if column != "" && rule.Table != "conversation_report_context" {
				_, err = exec("UPDATE "+rule.Table+" SET "+column+"='missing' WHERE "+predicate, keys...)
				require.NoError(t, err)
			}
			if rule.ID == "conversation_report_context.missing_conversation" {
				recordID = owner + orphan.RecordSeparator + id /* id intentionally has no conversation row */
			}
			result, err := store.Apply(ctx, orphan.Request{RuleID: rule.ID, RecordID: recordID, OlderThan: cutoff, Lease: lease})
			require.NoError(t, err)
			require.True(t, result.Eligible, "rule must exercise eligible behavior")
			var count int
			require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+rule.Table+" WHERE "+predicate, keys...).Scan(&count))
			switch rule.Action {
			case orphan.SafeDelete:
				require.True(t, result.Deleted)
				require.Equal(t, 0, count)
			case orphan.SafeDetach:
				require.True(t, result.Detached)
				require.Equal(t, 1, count)
				var value sql.NullString
				require.NoError(t, db.QueryRow("SELECT "+rule.DetachColumn+" FROM "+rule.Table+" WHERE "+predicate, keys...).Scan(&value))
				require.False(t, value.Valid)
			case orphan.ReportOnly:
				require.Equal(t, orphan.ReportedOnly, result.Reason)
				require.False(t, result.Mutated)
				require.Equal(t, 1, count)
			}
		})
	}
}
