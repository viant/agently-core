package native_test

import (
	"context"
	"database/sql"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	maintenance "github.com/viant/agently-core/internal/store/maintenancelease"
	technical "github.com/viant/agently-core/internal/store/technicalmaintenance"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestNativeTechnicalMaintenance_SQLiteDeletesOldTechnicalStateAndPreservesSavedReports(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cutoff := evaluatedAt.Add(-30 * 24 * time.Hour)
	old := evaluatedAt.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	svc, db := newTechnicalRuntime(t, func(t *testing.T, db *sql.DB) {
		seedTechnicalConversation(t, db, "technical-interactive", false, old)
		seedTechnicalReportRun(t, db, "technical-run", "technical-interactive", "running", old)
		seedTechnicalExportJob(t, db, "technical-job", "technical-interactive", "technical-run", "queued", old, 0)
		seedTechnicalExportArtifact(t, db, "technical-artifact", "technical-job", old, 0)
		seedTechnicalAudit(t, db, "technical-audit", "technical-job", "technical-artifact", old)
		mustExecTechnical(t, db, `INSERT INTO report_shared_artifact
            (artifact_id, artifact_ref, owner_id, kind, lifecycle, source_artifact_id, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			"saved-report", "saved://report", "owner", "report", "saved", "report_logical-id", old, old)
	})

	candidates := listAllTechnicalCandidates(t, svc, technical.CandidateRequest{
		Scope: technical.Interactive, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 1,
	})
	wantKinds := []string{
		technical.ReportRun,
		technical.ExportJob,
		technical.Audit,
	}
	gotKinds := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		gotKinds = append(gotKinds, candidate.Kind)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("candidate kinds = %v, want %v; candidates=%#v", gotKinds, wantKinds, candidates)
	}

	dryRun, err := svc.Maintain(context.Background(), technical.Request{
		Kind: technical.ReportRun, Scope: technical.Interactive,
		RecordID: "technical-run", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: technical.DryRun,
	})
	if err != nil {
		t.Fatalf("dry-run technical maintenance: %v", err)
	}
	if !dryRun.Eligible || dryRun.Deleted || dryRun.DeletedRows != 0 || dryRun.Reason != "eligible" {
		t.Fatalf("dry-run result = %#v", dryRun)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", "technical-run", 1)

	deleted, err := svc.Maintain(context.Background(), technical.Request{
		Kind: technical.ReportRun, Scope: technical.Interactive,
		RecordID: "technical-run", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: technical.Delete, Lease: acquireTechnicalRuntimeLease(t, svc, "technical-delete", "worker"),
	})
	if err != nil {
		t.Fatalf("delete technical maintenance: %v", err)
	}
	if !deleted.Eligible || !deleted.Deleted || deleted.DeletedRows != 4 || deleted.Reason != "deleted" {
		t.Fatalf("delete result = %#v", deleted)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", "technical-run", 0)
	assertTechnicalRowCount(t, db, "report_export_job", "job_id", "technical-job", 0)
	assertTechnicalRowCount(t, db, "report_export_artifact", "artifact_id", "technical-artifact", 0)
	assertTechnicalRowCount(t, db, "report_audit_event", "event_id", "technical-audit", 0)
	assertTechnicalRowCount(t, db, "report_shared_artifact", "artifact_id", "saved-report", 1)
}

func TestNativeTechnicalMaintenance_SQLiteScopesTTLSessionAndTransactionalRecheck(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cutoff := evaluatedAt.Add(-30 * 24 * time.Hour)
	oldTime := evaluatedAt.Add(-60 * 24 * time.Hour)
	old := oldTime.Format(time.RFC3339)
	recent := evaluatedAt.Add(-time.Hour).Format(time.RFC3339)
	recentlyExpired := evaluatedAt.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	svc, db := newTechnicalRuntime(t, func(t *testing.T, db *sql.DB) {
		seedTechnicalConversation(t, db, "technical-scheduled", true, old)
		seedTechnicalReportRun(t, db, "scheduled-run", "technical-scheduled", "completed", old)
		seedTechnicalReportRun(t, db, "unclassified-run", "", "failed", old)

		// A positive TTL overrides general retention in both directions.
		seedTechnicalExportJob(t, db, "ttl-kept-job", "", "", "running", old, int64((90*24*time.Hour)/time.Second))
		seedTechnicalExportJob(t, db, "ttl-expired-job", "", "", "queued", recent, 1)

		seedTechnicalAudit(t, db, "audit-recheck", "", "", old)
		mustExecTechnical(t, db, `INSERT INTO session (id, user_id, provider, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"expired-session", "owner", "local", old, old, old)
		mustExecTechnical(t, db, `INSERT INTO session (id, user_id, provider, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"recently-expired-session", "owner", "local", old, recentlyExpired, recentlyExpired)
		mustExecTechnical(t, db, `INSERT INTO session (id, user_id, provider, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"live-session", "owner", "local", recent, recent, evaluatedAt.Add(time.Hour).Format(time.RFC3339))
	})

	scheduled := listAllTechnicalCandidates(t, svc, technical.CandidateRequest{
		Scope: technical.Scheduled, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 10,
	})
	if len(scheduled) != 1 || scheduled[0].Kind != technical.ReportRun || scheduled[0].RecordID != "scheduled-run" {
		t.Fatalf("scheduled candidates = %#v", scheduled)
	}

	unclassified := listAllTechnicalCandidates(t, svc, technical.CandidateRequest{
		Scope: technical.Unclassified, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 20,
	})
	found := map[string]string{}
	for _, candidate := range unclassified {
		found[candidate.RecordID] = candidate.Kind
	}
	want := map[string]string{
		"unclassified-run": technical.ReportRun,
		"ttl-expired-job":  technical.ExportJob,
		"audit-recheck":    technical.Audit,
		"expired-session":  technical.Session,
	}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("unclassified candidates = %v, want %v; all=%#v", found, want, unclassified)
	}
	if _, ok := found["ttl-kept-job"]; ok {
		t.Fatalf("positive TTL that has not expired was ignored: %#v", unclassified)
	}
	if _, ok := found["live-session"]; ok {
		t.Fatalf("unexpired session was selected: %#v", unclassified)
	}
	if _, ok := found["recently-expired-session"]; ok {
		t.Fatalf("session expired within the retention period was selected: %#v", unclassified)
	}

	// Revalidation uses the same cutoff inside the mutation transaction.
	mustExecTechnical(t, db, `UPDATE report_audit_event SET occurred_at = ? WHERE event_id = ?`, recent, "audit-recheck")
	rechecked, err := svc.Maintain(context.Background(), technical.Request{
		Kind: technical.Audit, Scope: technical.Unclassified,
		RecordID: "audit-recheck", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: technical.Delete, Lease: acquireTechnicalRuntimeLease(t, svc, "technical-recheck", "worker"),
	})
	if err != nil {
		t.Fatalf("recheck technical maintenance: %v", err)
	}
	if rechecked.Eligible || rechecked.Deleted || rechecked.Reason != "no_longer_eligible" {
		t.Fatalf("recheck result = %#v", rechecked)
	}
	assertTechnicalRowCount(t, db, "report_audit_event", "event_id", "audit-recheck", 1)

	// A session that was old enough during selection must be preserved if its
	// expiry is moved inside the retention window before the mutation starts.
	mustExecTechnical(t, db, `UPDATE session SET expires_at = ? WHERE id = ?`, recentlyExpired, "expired-session")
	sessionRechecked, err := svc.Maintain(context.Background(), technical.Request{
		Kind: technical.Session, Scope: technical.Unclassified,
		RecordID: "expired-session", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: technical.Delete, Lease: acquireTechnicalRuntimeLease(t, svc, "technical-session-recheck", "worker"),
	})
	if err != nil {
		t.Fatalf("recheck expired session maintenance: %v", err)
	}
	if sessionRechecked.Eligible || sessionRechecked.Deleted || sessionRechecked.Reason != "no_longer_eligible" {
		t.Fatalf("rechecked session result = %#v", sessionRechecked)
	}
	assertTechnicalRowCount(t, db, "session", "id", "expired-session", 1)
	mustExecTechnical(t, db, `UPDATE session SET expires_at = ? WHERE id = ?`, old, "expired-session")

	sessionResult, err := svc.Maintain(context.Background(), technical.Request{
		Kind: technical.Session, Scope: technical.Unclassified,
		RecordID: "expired-session", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: technical.Delete, Lease: acquireTechnicalRuntimeLease(t, svc, "technical-session", "worker"),
	})
	if err != nil || sessionResult == nil || !sessionResult.Deleted {
		t.Fatalf("expired session result=%#v err=%v", sessionResult, err)
	}
	assertTechnicalRowCount(t, db, "session", "id", "expired-session", 0)
	assertTechnicalRowCount(t, db, "session", "id", "recently-expired-session", 1)
	assertTechnicalRowCount(t, db, "session", "id", "live-session", 1)
}

func listAllTechnicalCandidates(t *testing.T, svc *technical.Store, request technical.CandidateRequest) []technical.Candidate {
	t.Helper()
	var result []technical.Candidate
	for {
		page, err := svc.List(context.Background(), request)
		if err != nil {
			t.Fatalf("List(%q): %v", request.AfterCursor, err)
		}
		if len(page) == 0 {
			return result
		}
		result = append(result, page...)
		next := page[len(page)-1].CursorID
		if next <= request.AfterCursor {
			t.Fatalf("technical cursor did not advance: previous=%q next=%q", request.AfterCursor, next)
		}
		request.AfterCursor = next
		if len(result) > 100 {
			t.Fatal("technical maintenance pagination did not terminate")
		}
	}
}

func seedTechnicalConversation(t *testing.T, db *sql.DB, id string, scheduled bool, at string) {
	t.Helper()
	mustExecTechnical(t, db, `INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, scheduled) VALUES (?, ?, ?, ?, ?, ?)`,
		id, at, at, "succeeded", "owner", scheduled)
}

func seedTechnicalReportRun(t *testing.T, db *sql.DB, id, conversationID, status, at string) {
	t.Helper()
	var conversation interface{}
	if conversationID != "" {
		conversation = conversationID
	}
	mustExecTechnical(t, db, `INSERT INTO report_run
        (report_run_id, owner_id, conversation_id, materializer, status, started_at, completed_at, revision, ui_run_request_id, created_at, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "owner", conversation, "test", status, at, at, 1, "request-"+id, at, at)
}

func seedTechnicalExportJob(t *testing.T, db *sql.DB, id, conversationID, reportRunID, status, at string, ttlSeconds int64) {
	t.Helper()
	var conversation, reportRun, revision, exportRequest interface{}
	format, scope := "csv", "result"
	if conversationID != "" {
		conversation = conversationID
	}
	if reportRunID != "" {
		reportRun, revision, exportRequest = reportRunID, 1, "export-"+id
		format, scope = "pdf", "draft"
	}
	mustExecTechnical(t, db, `INSERT INTO report_export_job
        (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at, started_at, completed_at,
         retention_ttl_sec, report_run_id, report_run_revision, export_request_id)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "export://"+id, "owner", conversation, format, scope, status, at, at, at,
		ttlSeconds, reportRun, revision, exportRequest)
}

func seedTechnicalExportArtifact(t *testing.T, db *sql.DB, id, jobID, at string, ttlSeconds int64) {
	t.Helper()
	mustExecTechnical(t, db, `INSERT INTO report_export_artifact
        (artifact_id, job_id, artifact_ref, owner_id, format, content_type, created_at, retention_ttl_sec)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, jobID, "export://"+id, "owner", "pdf", "application/pdf", at, ttlSeconds)
}

func seedTechnicalAudit(t *testing.T, db *sql.DB, id, jobID, artifactID, at string) {
	t.Helper()
	var job, artifact interface{}
	if jobID != "" {
		job = jobID
	}
	if artifactID != "" {
		artifact = artifactID
	}
	mustExecTechnical(t, db, `INSERT INTO report_audit_event
        (event_id, event_type, artifact_ref, job_id, artifact_id, actor_id, occurred_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "audit://"+id, job, artifact, "owner", at)
}

func mustExecTechnical(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("seed technical maintenance fixture: %v\nSQL: %s", err, query)
	}
}

func assertTechnicalRowCount(t *testing.T, db *sql.DB, table, key, id string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+key+" = ?", id).Scan(&got); err != nil || got != want {
		t.Fatalf("%s.%s=%q count=%d want=%d err=%v", table, key, id, got, want, err)
	}
}

func newTechnicalRuntime(t *testing.T, seeds ...func(*testing.T, *sql.DB)) (*technical.Store, *sql.DB) {
	t.Helper()
	for _, key := range []string{"AGENTLY_DB_DRIVER", "AGENTLY_DB_DSN", "AGENTLY_DB_PATH", "AGENTLY_DB_SECRETS"} {
		t.Setenv(key, "")
	}
	_, source, _, _ := runtime.Caller(0)
	root := t.TempDir()
	server, err := native.New(context.Background(), native.Options{SourceRoot: filepath.Join(filepath.Dir(source), "../../.."), WorkspaceRoot: root})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(root, "db/agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for _, seed := range seeds {
		seed(t, db)
	}
	return &technical.Store{Invoker: server}, db
}
func acquireTechnicalRuntimeLease(t *testing.T, store *technical.Store, key, owner string) maintenance.Lease {
	t.Helper()
	result, err := (&maintenance.Store{Invoker: store.Invoker}).Acquire(context.Background(), key, owner, time.Minute)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	return result.Lease
}

func TestNativeTechnicalMaintenance_FencedRollbackAndReferenceGuards(t *testing.T) {
	evaluated := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cutoff := evaluated.Add(-30 * 24 * time.Hour)
	old := evaluated.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	recent := evaluated.Add(-time.Hour).Format(time.RFC3339)
	fixture := func(t *testing.T) (*technical.Store, *sql.DB) {
		return newTechnicalRuntime(t, func(t *testing.T, db *sql.DB) {
			seedTechnicalConversation(t, db, "guard-conversation", false, old)
			seedTechnicalReportRun(t, db, "guard-run", "guard-conversation", "running", old)
			seedTechnicalExportJob(t, db, "guard-job", "guard-conversation", "guard-run", "queued", old, 0)
			seedTechnicalExportArtifact(t, db, "guard-artifact", "guard-job", old, 0)
			seedTechnicalAudit(t, db, "guard-audit", "guard-job", "guard-artifact", old)
		})
	}
	for _, test := range []struct{ name, change string }{
		{"recent context", `INSERT INTO conversation_report_context(owner_id,conversation_id,active_report_run_id,revision,activation_source,actor_id,updated_at) VALUES('owner','guard-conversation','guard-run',1,'test','owner',?)`},
		{"recent dependent job", `UPDATE report_export_job SET completed_at=? WHERE job_id='guard-job'`},
		{"recent dependent artifact", `UPDATE report_export_artifact SET created_at=? WHERE artifact_id='guard-artifact'`},
		{"recent dependent audit", `UPDATE report_audit_event SET occurred_at=? WHERE event_id='guard-audit'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, db := fixture(t)
			mustExecTechnical(t, db, test.change, recent)
			lease := acquireTechnicalRuntimeLease(t, store, "technical-reference-guard", "worker")
			result, err := store.Maintain(context.Background(), technical.Request{Kind: technical.ReportRun, Scope: technical.Interactive, RecordID: "guard-run", OlderThan: cutoff, EvaluatedAt: evaluated, Mode: technical.Delete, Lease: lease})
			require.NoError(t, err)
			require.False(t, result.Eligible)
			require.Equal(t, "no_longer_eligible", result.Reason)
			assertTechnicalRowCount(t, db, "report_run", "report_run_id", "guard-run", 1)
		})
	}
	t.Run("late failure restores all rows and lease fence", func(t *testing.T) {
		store, db := fixture(t)
		lease := acquireTechnicalRuntimeLease(t, store, "technical-rollback", "worker")
		mustExecTechnical(t, db, `CREATE TRIGGER reject_technical BEFORE DELETE ON report_run WHEN OLD.report_run_id='guard-run' BEGIN SELECT RAISE(ABORT,'late technical failure'); END`)
		before := fullTreeSnapshot(t, db)
		var leaseBefore string
		require.NoError(t, db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM maintenance_lease WHERE lease_key=?`, lease.Key).Scan(&leaseBefore))
		_, err := store.Maintain(context.Background(), technical.Request{Kind: technical.ReportRun, Scope: technical.Interactive, RecordID: "guard-run", OlderThan: cutoff, EvaluatedAt: evaluated, Mode: technical.Delete, Lease: lease})
		require.ErrorContains(t, err, "late technical failure")
		require.Equal(t, before, fullTreeSnapshot(t, db))
		var leaseAfter string
		require.NoError(t, db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM maintenance_lease WHERE lease_key=?`, lease.Key).Scan(&leaseAfter))
		require.Equal(t, leaseBefore, leaseAfter)
	})
	t.Run("lost lease prevents deletion", func(t *testing.T) {
		store, db := fixture(t)
		lease := acquireTechnicalRuntimeLease(t, store, "technical-fence-loss", "worker")
		mustExecTechnical(t, db, `UPDATE maintenance_lease SET owner_id='other',lease_token='other-token' WHERE lease_key=?`, lease.Key)
		before := fullTreeSnapshot(t, db)
		_, err := store.Maintain(context.Background(), technical.Request{Kind: technical.ReportRun, Scope: technical.Interactive, RecordID: "guard-run", OlderThan: cutoff, EvaluatedAt: evaluated, Mode: technical.Delete, Lease: lease})
		require.ErrorIs(t, err, maintenance.ErrLeaseLost)
		require.Equal(t, before, fullTreeSnapshot(t, db))
	})
}
