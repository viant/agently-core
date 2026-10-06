package native_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	tree "github.com/viant/agently-core/internal/store/conversationtree"
	"github.com/viant/datly/standalone"
)

func fullTreeDeleteFixture(t *testing.T, legacy bool) (*standalone.Server, *sql.DB, context.Context) {
	t.Helper()
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	_, source, _, _ := runtime.Caller(0)
	root := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(source), "../../.."), WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite", filepath.Join(root, "db/agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`
 CREATE TABLE IF NOT EXISTS investigation(id TEXT PRIMARY KEY,title TEXT,created_by TEXT,conversation_id TEXT,summary TEXT,ad_order_id INTEGER,verdict TEXT,created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,redacted) VALUES('plan-payload','request','text/plain',1,'inline','x','none',0);
 INSERT INTO conversation(id,created_by_user_id,status,schedule_run_id) VALUES('plan-root','u1','succeeded','dangling-legacy'),('plan-outside','u2','succeeded',NULL);
 INSERT INTO turn(id,conversation_id,status,run_id) VALUES('plan-turn','plan-root','succeeded','dangling-run'),('plan-outside-turn','plan-outside','succeeded',NULL);
 INSERT INTO message(id,conversation_id,turn_id,role,type,attachment_payload_id) VALUES('plan-model','plan-root','plan-turn','assistant','text','plan-payload'),('plan-tool','plan-root','plan-turn','tool','tool_op',NULL),('plan-outside-message','plan-outside','plan-outside-turn','assistant','text',NULL);
 UPDATE message SET parent_message_id='plan-model',superseded_by='plan-tool' WHERE id='plan-outside-message';
 UPDATE message SET parent_message_id='plan-model' WHERE id='plan-tool';
 UPDATE turn SET started_by_message_id='plan-model',retry_of='plan-turn' WHERE id='plan-outside-turn';
 INSERT INTO goal(id,conversation_id,objective,status,created_at,updated_at) VALUES('plan-goal','plan-root','test','completed','2026-01-01','2026-01-01');
 INSERT INTO schedule(id,name,agent_ref,created_by_user_id,conversation_id,goal_id,internal,schedule_type) VALUES('goal-wakeup-plan-goal','autonomous::goal-wakeup::plan-goal','test','u1','plan-root','plan-goal',1,'adhoc');
 INSERT INTO run(id,schedule_id,status,resumed_from_run_id) VALUES('plan-wakeup-run','goal-wakeup-plan-goal','succeeded',NULL),('plan-outside-run',NULL,'succeeded','dangling-run'),('plan-extra',NULL,'succeeded',NULL);
 INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,run_id) VALUES('plan-model','plan-turn','test','test','chat','succeeded','dangling-model-run');
 INSERT INTO tool_call(message_id,turn_id,op_id,tool_name,tool_kind,status,run_id) VALUES('plan-tool','plan-turn','op','test','function','succeeded','dangling-tool-run');
 INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status) VALUES('plan-queue','plan-root','plan-turn','plan-model',1,'completed');
 INSERT INTO generated_file(id,conversation_id,turn_id,message_id,provider,mode,copy_mode,filename,mime_type,payload_id) VALUES('plan-file','plan-root','plan-turn','plan-model','test','test','test','file','text/plain','plan-payload');
 INSERT INTO investigation(id,conversation_id,created_by) VALUES('plan-investigation','plan-root','u1');
 INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state) VALUES('plan-claim','rule','test','plan-turn','hash','completed');
 INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id) VALUES('plan-report','u1','plan-root','test','completed','2026-01-01',2,'request');
 INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id) VALUES('u1','plan-root','plan-report',3,'test','u1');
 INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,report_run_id,report_run_revision,export_request_id,format,scope,status) VALUES('plan-job','report://plan','u1','plan-root','plan-report',2,'export-request','pdf','draft','succeeded');
 INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type) VALUES('plan-artifact','plan-job','report://plan','u1','pdf','application/pdf');
 INSERT INTO report_audit_event(event_id,event_type,artifact_ref,job_id,artifact_id,actor_id) VALUES('plan-audit-job','test','report://plan','plan-job',NULL,'u1'),('plan-audit-artifact','test','report://plan',NULL,'plan-artifact','u2');
 
 INSERT INTO users(id,username) VALUES('u1','u1'),('u2','u2');
 INSERT INTO conversation(id,created_by_user_id,status,conversation_parent_id) VALUES('tree-child','u1','succeeded','plan-root');
 INSERT INTO run(id,conversation_id,turn_id,status) VALUES('tree-current','plan-root','plan-turn','succeeded');
 INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,compression,redacted) VALUES('tree-private','request','text/plain',1,'inline','p','none',0),('tree-unrelated','request','text/plain',1,'inline','u','none',0);
 UPDATE tool_call SET request_payload_id='tree-private' WHERE message_id='plan-tool';
 UPDATE message SET attachment_payload_id='plan-payload',updated_at='2026-01-05 01:02:03' WHERE id='plan-outside-message';
 INSERT INTO tool_approval_queue(id,user_id,conversation_id,tool_name,arguments,status) VALUES('tree-approval-conversation','u1','plan-root','test',X'7B7D','pending'),('tree-approval-unrelated','u2','plan-outside','test',X'7B7D','pending');
 INSERT INTO tool_approval_queue(id,user_id,turn_id,tool_name,arguments,status) VALUES('tree-approval-turn','u1','plan-turn','test',X'7B7D','pending');
 INSERT INTO tool_approval_queue(id,user_id,message_id,tool_name,arguments,status) VALUES('tree-approval-message','u1','plan-model','test',X'7B7D','pending');
 INSERT INTO goal(id,conversation_id,objective,status,created_at,updated_at) VALUES('tree-outside-goal','plan-outside','retain','completed','2026-01-01','2026-01-01');
 INSERT INTO schedule(id,name,agent_ref,created_by_user_id,conversation_id,goal_id,internal,schedule_type) VALUES('tree-outside-schedule','Outside','test','u2','plan-outside','tree-outside-goal',0,'adhoc');
 INSERT INTO model_call(message_id,turn_id,provider,model,model_kind,status,run_id) VALUES('plan-outside-message','plan-outside-turn','test','test','chat','succeeded','plan-extra');
 INSERT INTO tool_call(message_id,turn_id,op_id,tool_name,tool_kind,status,run_id) VALUES('plan-outside-message','plan-outside-turn','outside-op','test','function','succeeded','plan-extra');
 INSERT INTO turn_queue(id,conversation_id,turn_id,message_id,queue_seq,status) VALUES('tree-outside-queue','plan-outside','plan-outside-turn','plan-outside-message',1,'completed');
 INSERT INTO generated_file(id,conversation_id,turn_id,message_id,provider,mode,copy_mode,filename,mime_type,payload_id) VALUES('tree-outside-file','plan-outside','plan-outside-turn','plan-outside-message','test','test','test','outside-file','text/plain','tree-unrelated');
 INSERT INTO investigation(id,conversation_id,created_by) VALUES('tree-outside-investigation','plan-outside','u2');
 INSERT INTO tool_execution_claim(claim_key,rule_id,canonical_tool_name,turn_id,semantic_request_hash,state) VALUES('tree-outside-claim','rule','test','plan-outside-turn','outside-hash','completed');
 INSERT INTO report_run(report_run_id,owner_id,conversation_id,materializer,status,started_at,revision,ui_run_request_id) VALUES('tree-outside-report','u2','plan-outside','test','completed','2026-01-01',1,'outside-request');
 INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id) VALUES('u2','plan-outside','tree-outside-report',1,'test','u2');
 INSERT INTO report_export_job(job_id,artifact_ref,owner_id,conversation_id,report_run_id,report_run_revision,export_request_id,format,scope,status) VALUES('tree-outside-job','report://outside','u2','plan-outside','tree-outside-report',1,'outside-export','pdf','draft','succeeded');
 INSERT INTO report_export_artifact(artifact_id,job_id,artifact_ref,owner_id,format,content_type) VALUES('tree-outside-artifact','tree-outside-job','report://outside','u2','pdf','application/pdf');
 INSERT INTO report_audit_event(event_id,event_type,artifact_ref,job_id,artifact_id,actor_id) VALUES('tree-outside-audit','test','report://outside','tree-outside-job','tree-outside-artifact','u2');

`)
	require.NoError(t, err)
	if legacy {
		_, err = db.Exec(`
 CREATE TABLE schedule_run(id TEXT PRIMARY KEY,schedule_id TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME,status TEXT NOT NULL DEFAULT 'pending',error_message TEXT,lease_owner TEXT,lease_until DATETIME,precondition_ran_at DATETIME,precondition_passed INTEGER,precondition_result TEXT,conversation_id TEXT,conversation_kind TEXT DEFAULT 'scheduled',scheduled_for DATETIME,started_at DATETIME,completed_at DATETIME);
 INSERT INTO schedule_run(id,schedule_id,conversation_id,status) VALUES('tree-legacy','goal-wakeup-plan-goal','plan-root','succeeded');
 `)
		require.NoError(t, err)
	}
	return server, db, authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
}

// These tests exercise real linked application component transactions on
// SQLite. They do not establish MySQL locking or contention safety.
func TestWorkspaceRuntimeFullConversationTreeDeletion(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_table_%v", legacy), func(t *testing.T) {
			server, db, ctx := fullTreeDeleteFixture(t, legacy)
			now := time.Now().UTC()
			require.NoError(t, tree.InvokeDelete(ctx, server, &tree.DeleteInput{RootIDs: []string{"plan-root"}, Now: &now}))
			for _, table := range []string{"turn_queue", "model_call", "tool_call", "generated_file", "investigation", "tool_execution_claim", "goal", "schedule", "report_run", "conversation_report_context", "report_export_job", "report_export_artifact", "report_audit_event"} {
				fullTreeCount(t, db, table, 1)
			}
			fullTreeCount(t, db, "conversation", 1)
			fullTreeCount(t, db, "turn", 1)
			fullTreeCount(t, db, "message", 1)
			fullTreeCount(t, db, "run", 2)
			fullTreeCount(t, db, "tool_approval_queue", 1)
			if legacy {
				fullTreeCount(t, db, "schedule_run", 0)
			}
			var resumed, parent, superseded, starter, retry any
			var updated string
			require.NoError(t, db.QueryRow(`SELECT resumed_from_run_id FROM run WHERE id='plan-outside-run'`).Scan(&resumed))
			require.NoError(t, db.QueryRow(`SELECT parent_message_id,superseded_by,CAST(updated_at AS TEXT) FROM message WHERE id='plan-outside-message'`).Scan(&parent, &superseded, &updated))
			require.NoError(t, db.QueryRow(`SELECT started_by_message_id,retry_of FROM turn WHERE id='plan-outside-turn'`).Scan(&starter, &retry))
			require.Nil(t, resumed)
			require.Nil(t, parent)
			require.Nil(t, superseded)
			require.Nil(t, starter)
			require.Nil(t, retry)
			require.Equal(t, "2026-01-05 01:02:03", updated)
			fullTreeCount(t, db, "call_payload", 2)
			var ids string
			require.NoError(t, db.QueryRow(`SELECT GROUP_CONCAT(id) FROM (SELECT id FROM call_payload ORDER BY id)`).Scan(&ids))
			require.Equal(t, "plan-payload,tree-unrelated", ids)
		})
	}
}

func TestWorkspaceRuntimeFullConversationTreeRollback(t *testing.T) {
	for _, table := range []string{"conversation", "call_payload"} {
		t.Run("late_"+table, func(t *testing.T) {
			server, db, ctx := fullTreeDeleteFixture(t, true)
			trigger := `CREATE TRIGGER reject_tree_delete BEFORE DELETE ON conversation WHEN OLD.id='plan-root' BEGIN SELECT RAISE(ABORT,'late tree failure'); END`
			if table == "call_payload" {
				trigger = `CREATE TRIGGER reject_tree_delete BEFORE DELETE ON call_payload WHEN OLD.id='tree-private' BEGIN SELECT RAISE(ABORT,'late tree failure'); END`
			}
			_, err := db.Exec(trigger)
			require.NoError(t, err)
			before := fullTreeSnapshot(t, db)
			err = tree.InvokeDelete(ctx, server, &tree.DeleteInput{RootIDs: []string{"plan-root"}})
			require.ErrorContains(t, err, "late tree failure")
			require.Equal(t, before, fullTreeSnapshot(t, db), "all earlier deletes and detaches must roll back")
		})
	}
}

func TestWorkspaceRuntimeFullConversationTreeProtection(t *testing.T) {
	cases := []struct {
		name, sql string
		expected  error
	}{
		{"wrong root owner", `UPDATE conversation SET created_by_user_id='u2' WHERE id='plan-root'`, tree.ErrPermissionDenied},
		{"foreign descendant", `UPDATE conversation SET created_by_user_id='u2' WHERE id='tree-child'`, tree.ErrPermissionDenied},
		{"external linked conversation", `UPDATE message SET linked_conversation_id='plan-root' WHERE id='plan-outside-message'`, tree.ErrGraphReferenced},
		{"malformed lease", `UPDATE run SET status='running',lease_until='malformed',last_heartbeat_at=NULL WHERE id='tree-current'`, tree.ErrConversationActive},
		{"live lease", `UPDATE run SET status='running',lease_until='2099-01-01' WHERE id='tree-current'`, tree.ErrConversationActive},
		{"fresh heartbeat", `UPDATE run SET status='running',lease_until=NULL,last_heartbeat_at=CURRENT_TIMESTAMP,heartbeat_interval_sec=5 WHERE id='tree-current'`, tree.ErrConversationActive},
		{"unknown active status", `UPDATE conversation SET status='unexpected' WHERE id='plan-root'`, tree.ErrNonTerminal},
		{"report ownership", `UPDATE report_run SET owner_id='u2' WHERE report_run_id='plan-report'`, tree.ErrPermissionDenied},
		{"outside report context", `INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id) VALUES('u1','plan-outside','plan-report',1,'test','u1')`, tree.ErrGraphReferenced},
		{"running export", `UPDATE report_export_job SET status='running' WHERE job_id='plan-job'`, tree.ErrConversationActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, db, ctx := fullTreeDeleteFixture(t, false)
			_, err := db.Exec(tc.sql)
			require.NoError(t, err)
			before := fullTreeSnapshot(t, db)
			err = tree.InvokeDelete(ctx, server, &tree.DeleteInput{RootIDs: []string{"plan-root"}})
			require.ErrorIs(t, err, tc.expected)
			require.Equal(t, before, fullTreeSnapshot(t, db))
		})
	}
}

func TestWorkspaceRuntimeFullConversationTreeMalformedHeartbeatIsStale(t *testing.T) {
	server, db, ctx := fullTreeDeleteFixture(t, false)
	_, err := db.Exec(`UPDATE run SET status='running',lease_until=NULL,last_heartbeat_at='malformed' WHERE id='tree-current'`)
	require.NoError(t, err)
	require.NoError(t, tree.InvokeDelete(ctx, server, &tree.DeleteInput{RootIDs: []string{"plan-root"}}))
	fullTreeCount(t, db, "conversation", 1)
}

func TestWorkspaceRuntimeFullConversationTreeMalformedLegacyLeaseBlocks(t *testing.T) {
	server, db, ctx := fullTreeDeleteFixture(t, true)
	_, err := db.Exec(`UPDATE schedule_run SET status='running',lease_until='malformed'`)
	require.NoError(t, err)
	before := fullTreeSnapshot(t, db)
	require.ErrorIs(t, tree.InvokeDelete(ctx, server, &tree.DeleteInput{RootIDs: []string{"plan-root"}}), tree.ErrConversationActive)
	require.Equal(t, before, fullTreeSnapshot(t, db))
}

func fullTreeCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
	require.Equal(t, want, count, table)
}
func fullTreeSnapshot(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	result := map[string][][]any{}
	for _, table := range []string{"conversation", "turn", "message", "run", "goal", "schedule", "turn_queue", "model_call", "tool_call", "generated_file", "investigation", "tool_execution_claim", "tool_approval_queue", "report_run", "conversation_report_context", "report_export_job", "report_export_artifact", "report_audit_event", "call_payload", "schedule_run"} {
		var exists int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists))
		if exists == 0 {
			continue
		}
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY 1")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		values := [][]any{}
		for rows.Next() {
			row := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range row {
				dest[i] = &row[i]
			}
			require.NoError(t, rows.Scan(dest...))
			for i, value := range row {
				if bytes, ok := value.([]byte); ok {
					row[i] = string(bytes)
				}
			}
			values = append(values, row)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		result[table] = values
	}
	return result
}
