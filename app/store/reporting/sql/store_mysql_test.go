package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	authctx "github.com/viant/agently-core/internal/auth"
	reportartifactmodel "github.com/viant/agently-core/model/reportartifact"
	reportcontextmodel "github.com/viant/agently-core/model/reportcontext"
	reportjobmodel "github.com/viant/agently-core/model/reportjob"
	reportrunmodel "github.com/viant/agently-core/model/reportrun"
	reporting "github.com/viant/agently-core/service/reporting"
	"github.com/viant/datly/bootstrap/connector"
)

func TestNativeReportingMySQLIndependentConnectionsAdoptionExportAndAudit(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	ctx := context.Background()
	db, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	prefix := fmt.Sprintf("reportmysql-%d-", time.Now().UnixNano())
	ownerID := prefix + "owner"
	t.Cleanup(func() {
		_, err := db.Exec("DELETE FROM report_audit_event WHERE actor_id=?", ownerID)
		require.NoError(t, err)
		for _, item := range []struct{ table, key string }{{"report_export_artifact", "artifact_id"}, {"report_export_job", "job_id"}, {"conversation_report_context", "conversation_id"}, {"report_run", "report_run_id"}, {"conversation", "id"}} {
			_, err := db.Exec("DELETE FROM "+item.table+" WHERE "+item.key+" LIKE ?", prefix+"%")
			require.NoError(t, err)
		}
	})
	_, file, _, _ := runtime.Caller(0)
	stores := make([]*Store, 2)
	for i := range stores {
		server, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(file), "../../../.."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
		client, err := New(ctx, server, nil, nil)
		require.NoError(t, err)
		stores[i] = client.(*Store)
	}
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: ownerID})
	conversationID := prefix + "conversation"
	_, err = db.Exec("INSERT INTO conversation(id,created_by_user_id,status) VALUES(?,?,'succeeded')", conversationID, ownerID)
	require.NoError(t, err)
	at := time.Now().UTC().Truncate(time.Second)
	run := &reportrunmodel.Record{ReportRunID: prefix + "run", OwnerID: ownerID, Materializer: reportrunmodel.MaterializerLegacyBrowser, Origin: "manual", Status: "completed", StartedAt: at, CompletedAt: &at, Revision: 2, UIRunRequestID: prefix + "request", ReportSpec: []byte(`{"kind":"reportSpec","version":1}`), ReportFill: []byte(`{"kind":"reportFill","datasets":[]}`), ReportPrint: []byte(`{"kind":"reportPrint","pages":[]}`), CreatedAt: at, UpdatedAt: at}
	require.NoError(t, stores[0].CreateReportRun(owner, run))
	run, err = stores[0].GetReportRun(owner, run.ReportRunID)
	require.NoError(t, err)
	race := func(action func(int) error) []error {
		start := make(chan struct{})
		results := make([]error, 2)
		var group sync.WaitGroup
		for i := range results {
			group.Add(1)
			go func(index int) { defer group.Done(); <-start; results[index] = action(index) }(i)
		}
		close(start)
		group.Wait()
		return results
	}
	adoption := race(func(i int) error {
		next := *run
		next.ConversationID = conversationID
		next.ActorID = ownerID
		next.AdoptionSource = "adopt"
		next.Revision = 3
		next.UpdatedAt = at.Add(time.Second)
		pointer := &reportcontextmodel.Record{OwnerID: ownerID, ConversationID: conversationID, ActiveReportRunID: run.ReportRunID, Revision: 1, ActivationSource: "adopt", ActorID: ownerID, UpdatedAt: next.UpdatedAt}
		return stores[i].AdoptReportRunAndContextCAS(owner, &next, 2, pointer, 0)
	})
	success := 0
	for _, err := range adoption {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, reportstore.ErrCASMismatch)
		}
	}
	require.Equal(t, 1, success)
	adopted, err := stores[0].GetReportRun(owner, run.ReportRunID)
	require.NoError(t, err)
	require.Equal(t, int64(3), adopted.Revision)
	pointer, err := stores[0].GetConversationReportContext(owner, conversationID)
	require.NoError(t, err)
	require.Equal(t, int64(1), pointer.Revision)
	jobs := make([]*reportjobmodel.Record, 2)
	submit := race(func(i int) error {
		candidate := &reportjobmodel.Record{JobID: fmt.Sprintf("%sjob-%d", prefix, i), ArtifactRef: "report-run://" + run.ReportRunID, OwnerID: ownerID, ConversationID: conversationID, ReportRunID: run.ReportRunID, ExportRequestID: prefix + "operation", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at.Add(2 * time.Second)}
		var err error
		jobs[i], _, err = stores[i].SubmitJobFromRun(owner, candidate)
		return err
	})
	for _, err := range submit {
		require.NoError(t, err)
	}
	require.Equal(t, jobs[0].JobID, jobs[1].JobID)
	worker := WithInternalAccess(ctx)
	_, err = stores[0].ClaimJob(worker, jobs[0].JobID, at.Add(3*time.Second))
	require.NoError(t, err)
	outputs := make([]*reportjobmodel.Record, 2)
	completion := race(func(i int) error {
		artifact := &reportartifactmodel.Record{ArtifactID: fmt.Sprintf("%sartifact-%d", prefix, i), JobID: jobs[0].JobID, OwnerID: ownerID, Format: "pdf", ContentType: "application/pdf", Data: []byte("%PDF MySQL contention"), CreatedAt: at.Add(4 * time.Second)}
		var err error
		outputs[i], err = stores[i].CompleteJobWithArtifact(worker, jobs[0].JobID, artifact, nil, at.Add(5*time.Second), 0)
		return err
	})
	for _, err := range completion {
		require.NoError(t, err)
	}
	require.Equal(t, outputs[0].ArtifactID, outputs[1].ArtifactID)
	var artifacts int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE job_id=?", jobs[0].JobID).Scan(&artifacts))
	require.Equal(t, 1, artifacts)
	var group sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			errs[index] = NewAuditSink(stores[index%2]).Record(owner, &reporting.AuditEvent{EventType: "report.download", ArtifactRef: jobs[0].ArtifactRef, JobID: jobs[0].JobID, ArtifactID: outputs[0].ArtifactID, ActorID: ownerID, OccurredAt: at.Add(6 * time.Second), Metadata: map[string]any{"index": index}})
		}(i)
	}
	group.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	var audits int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM report_audit_event WHERE actor_id=? AND event_type='report.download'", ownerID).Scan(&audits))
	require.Equal(t, len(errs), audits)
	// A failure in the final job writer must roll back the earlier artifact insert.
	lateCandidate := &reportjobmodel.Record{JobID: prefix + "late-job", ArtifactRef: "report-run://" + run.ReportRunID, OwnerID: ownerID, ConversationID: conversationID, ReportRunID: run.ReportRunID, ExportRequestID: prefix + "late-operation", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at.Add(7 * time.Second)}
	lateJob, _, err := stores[0].SubmitJobFromRun(owner, lateCandidate)
	require.NoError(t, err)
	_, err = stores[0].ClaimJob(worker, lateJob.JobID, at.Add(8*time.Second))
	require.NoError(t, err)
	trigger := fmt.Sprintf("reportmysql_reject_%d", time.Now().UnixNano())
	_, err = db.Exec(fmt.Sprintf("CREATE TRIGGER `%s` BEFORE UPDATE ON report_export_job FOR EACH ROW BEGIN IF NEW.job_id='%s' AND NEW.status='succeeded' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='late reporting fixture rejection'; END IF; END", trigger, lateJob.JobID))
	require.NoError(t, err)
	t.Cleanup(func() { _, err := db.Exec("DROP TRIGGER IF EXISTS `" + trigger + "`"); require.NoError(t, err) })
	_, err = stores[0].CompleteJobWithArtifact(worker, lateJob.JobID, &reportartifactmodel.Record{ArtifactID: prefix + "late-artifact", JobID: lateJob.JobID, OwnerID: ownerID, Format: "pdf", ContentType: "application/pdf", Data: []byte("%PDF must rollback"), CreatedAt: at.Add(9 * time.Second)}, nil, at.Add(10*time.Second), 0)
	require.ErrorContains(t, err, "late reporting fixture rejection")
	var lateArtifacts int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM report_export_artifact WHERE job_id=?", lateJob.JobID).Scan(&lateArtifacts))
	require.Zero(t, lateArtifacts)
	stillRunning, err := stores[0].GetJob(worker, lateJob.JobID)
	require.NoError(t, err)
	require.Equal(t, "running", stillRunning.Status)

}
