package native_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	authctx "github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/store/conversationtree"
)

func TestWorkspaceRuntimeConversationTreeDeletionPlan(t *testing.T) {
	for _, name := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(name, "")
	}
	_, file, _, _ := runtime.Caller(0)
	workspace := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "..", "..", ".."), WorkspaceRoot: workspace})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspace, "db", "agently-core.db"))
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
 `)
	require.NoError(t, err)
	owner := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "u1"})
	d := &conversationtree.Discoverer{Invoker: server, OwnerID: authctx.EffectiveUserID}
	graph, err := d.DiscoverAuthorized(owner, "plan-root")
	require.NoError(t, err)
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	plan, err := d.CollectDeletePlan(owner, graph, now, []string{"plan-extra"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"plan-root"}, plan.ConversationIDs)
	require.Equal(t, []string{"plan-turn"}, plan.TurnIDs)
	require.Equal(t, []string{"plan-model", "plan-tool"}, plan.MessageIDs)
	require.Equal(t, []string{"dangling-model-run", "dangling-run", "dangling-tool-run", "plan-extra", "plan-wakeup-run"}, plan.RunIDs)
	require.Equal(t, []string{"dangling-legacy"}, plan.ScheduleRunIDs)
	require.False(t, plan.Tables["schedule_run"])
	require.Equal(t, []string{"goal-wakeup-plan-goal"}, plan.ScheduleIDs)
	require.Equal(t, []string{"plan-payload"}, plan.PayloadIDs)
	require.Len(t, plan.Goals, 1)
	require.Len(t, plan.Schedules, 1)
	require.Len(t, plan.ModelCalls, 1)
	require.Len(t, plan.ToolCalls, 1)
	require.Len(t, plan.Queues, 1)
	require.Len(t, plan.GeneratedFiles, 1)
	require.Len(t, plan.Investigations, 1)
	require.Len(t, plan.Claims, 1)
	require.Len(t, plan.ReportRuns, 1)
	require.Len(t, plan.ReportContexts, 1)
	require.Equal(t, int64(3), plan.ReportContexts[0].Revision)
	require.Len(t, plan.ReportJobs, 1)
	require.Len(t, plan.ReportArtifacts, 1)
	require.Len(t, plan.ReportAuditEvents, 2)
	require.Len(t, plan.DetachRuns, 1)
	require.Equal(t, "plan-outside-run", plan.DetachRuns[0].Id)
	require.Len(t, plan.DetachMessages, 2, "inside and outside link clearing snapshots")
	require.Len(t, plan.DetachTurns, 1)
	require.Equal(t, "plan-outside-turn", plan.DetachTurns[0].Id)
	// Repeated refresh retains raw dangling IDs and avoids duplicate detach rows.
	require.NoError(t, d.RefreshDeletePlanRunEvidence(owner, plan))
	require.Len(t, plan.DetachMessages, 2)
	require.Contains(t, plan.RunIDs, "dangling-run")
	empty, err := d.CollectDeletePlan(owner, &conversationtree.Graph{Nodes: map[string]*conversationtree.Node{}}, now, []string{"plan-extra"}, nil)
	require.NoError(t, err)
	require.Empty(t, empty.ConversationIDs)
	require.Equal(t, []string{"plan-extra"}, empty.RunIDs)
	require.Len(t, empty.Runs, 1)
	// A legacy table installed by the host extension is discovered explicitly;
	// wakeup-schedule rows join the plan even without a graph conversation link.
	_, err = db.Exec(`CREATE TABLE schedule_run(id TEXT PRIMARY KEY,schedule_id TEXT NOT NULL,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME,status TEXT NOT NULL DEFAULT 'pending',error_message TEXT,lease_owner TEXT,lease_until DATETIME,precondition_ran_at DATETIME,precondition_passed INTEGER,precondition_result TEXT,conversation_id TEXT,conversation_kind TEXT DEFAULT 'scheduled',scheduled_for DATETIME,started_at DATETIME,completed_at DATETIME);
 INSERT INTO schedule_run(id,schedule_id,status) VALUES('plan-legacy-wakeup','goal-wakeup-plan-goal','succeeded'),('plan-legacy-extra','other-schedule','succeeded')`)
	require.NoError(t, err)
	withLegacy, err := d.CollectDeletePlan(owner, graph, now, nil, []string{"plan-legacy-extra"})
	require.NoError(t, err)
	require.True(t, withLegacy.Tables["schedule_run"])
	require.Equal(t, []string{"dangling-legacy", "plan-legacy-extra", "plan-legacy-wakeup"}, withLegacy.ScheduleRunIDs)
	require.Len(t, withLegacy.LegacyRuns, 2)

	// The protected lock mode cannot execute outside its parent's managed tx.
	d.LockDetachRows = true
	_, err = d.CollectDeletePlan(owner, graph, now, nil, nil)
	require.ErrorContains(t, err, "FOR UPDATE requires an active transaction")

	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM conversation WHERE id='plan-root'`).Scan(&count))
	require.Equal(t, 1, count, "plan collection performs no mutation")
}
