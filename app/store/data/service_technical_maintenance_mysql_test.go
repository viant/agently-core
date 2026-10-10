package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/agently-core/app/store/native"
	"github.com/viant/datly/bootstrap/connector"
	"path/filepath"
	"runtime"
)

func TestTechnicalMaintenance_MySQLDeletesTechnicalStateAndPreservesSavedReports(t *testing.T) {
	dsn := os.Getenv("AGENTLY_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("AGENTLY_TEST_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open(mysql): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err = db.PingContext(ctx); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	conversationID := "000-technical-mysql-conversation-" + suffix
	runID := "000-technical-mysql-run-" + suffix
	jobID := "000-technical-mysql-job-" + suffix
	artifactID := "000-technical-mysql-artifact-" + suffix
	auditID := "000-technical-mysql-audit-" + suffix
	sharedID := "000-technical-mysql-shared-" + suffix
	sessionID := "000-technical-mysql-session-" + suffix
	recentSessionID := "000-technical-mysql-recent-session-" + suffix
	leaseKey := "test-technical-maintenance-" + suffix
	t.Cleanup(func() {
		statements := []struct {
			query string
			id    string
		}{
			{`DELETE FROM report_audit_event WHERE event_id = ?`, auditID},
			{`DELETE FROM report_export_artifact WHERE artifact_id = ?`, artifactID},
			{`DELETE FROM report_export_job WHERE job_id = ?`, jobID},
			{`DELETE FROM conversation_report_context WHERE active_report_run_id = ?`, runID},
			{`DELETE FROM report_run WHERE report_run_id = ?`, runID},
			{`DELETE FROM report_shared_artifact WHERE artifact_id = ?`, sharedID},
			{`DELETE FROM session WHERE id = ?`, sessionID},
			{`DELETE FROM session WHERE id = ?`, recentSessionID},
			{`DELETE FROM conversation WHERE id = ?`, conversationID},
			{`DELETE FROM maintenance_lease WHERE lease_key = ?`, leaseKey},
		}
		for _, statement := range statements {
			if _, cleanupErr := db.Exec(statement.query, statement.id); cleanupErr != nil {
				t.Errorf("cleanup MySQL technical fixture with %q: %v", statement.query, cleanupErr)
			}
		}
	})

	evaluatedAt := time.Now().UTC().Truncate(time.Second)
	cutoff := evaluatedAt.Add(-30 * 24 * time.Hour)
	old := evaluatedAt.Add(-60 * 24 * time.Hour)
	recentlyExpired := evaluatedAt.Add(-10 * 24 * time.Hour)
	statements := []struct {
		query string
		args  []interface{}
	}{
		{`INSERT INTO conversation (id, created_at, updated_at, status, created_by_user_id, scheduled) VALUES (?, ?, ?, ?, ?, ?)`, []interface{}{conversationID, old, old, "succeeded", "owner", 0}},
		{`INSERT INTO report_run
            (report_run_id, owner_id, conversation_id, materializer, status, started_at, revision, ui_run_request_id, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{runID, "owner", conversationID, "test", "running", old, 1, "request-" + runID, old, old}},
		{`INSERT INTO report_export_job
            (job_id, artifact_ref, owner_id, conversation_id, format, scope, status, submitted_at,
             retention_ttl_sec, report_run_id, report_run_revision, export_request_id)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{jobID, "export://" + jobID, "owner", conversationID, "pdf", "draft", "queued", old, 0, runID, 1, "export-" + jobID}},
		{`INSERT INTO report_export_artifact
            (artifact_id, job_id, artifact_ref, owner_id, format, content_type, created_at, retention_ttl_sec)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{artifactID, jobID, "export://" + artifactID, "owner", "pdf", "application/pdf", old, 0}},
		{`INSERT INTO report_audit_event
            (event_id, event_type, artifact_ref, job_id, artifact_id, actor_id, occurred_at)
            VALUES (?, ?, ?, ?, ?, ?, ?)`, []interface{}{auditID, "test", "audit://" + auditID, jobID, artifactID, "owner", old}},
		{`INSERT INTO report_shared_artifact
            (artifact_id, artifact_ref, owner_id, kind, lifecycle, source_artifact_id, created_at, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, []interface{}{sharedID, "saved://" + sharedID, "owner", "report", "saved", "logical-report-source", old, old}},
		{`INSERT INTO session (id, user_id, provider, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`, []interface{}{sessionID, "owner", "local", old, old, old}},
		{`INSERT INTO session (id, user_id, provider, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`, []interface{}{recentSessionID, "owner", "local", old, recentlyExpired, recentlyExpired}},
	}
	for _, statement := range statements {
		if _, err = db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed MySQL technical fixture with %q: %v", statement.query, err)
		}
	}

	_, source, _, _ := runtime.Caller(0)
	server, err := native.New(ctx, native.Options{SourceRoot: filepath.Join(filepath.Dir(source), "../../.."), Connectors: []connector.Config{{Name: "agently", Driver: "mysql", DSN: dsn}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	svc := &datlyService{native: server}

	// Exercise every MySQL candidate query against exact fixture IDs. This is
	// deterministic even when the developer database contains unrelated rows.
	checks := []struct {
		kind     TechnicalMaintenanceKind
		priority int
		id       string
	}{
		{TechnicalMaintenanceReportRun, 10, runID},
		{TechnicalMaintenanceReportExportJob, 20, jobID},
		{TechnicalMaintenanceReportAudit, 30, auditID},
		{TechnicalMaintenanceSession, 40, sessionID},
	}
	for _, check := range checks {
		scope := TechnicalMaintenanceInteractive
		if check.kind == TechnicalMaintenanceSession {
			scope = TechnicalMaintenanceUnclassified
		}
		result, err := svc.MaintainTechnicalCandidate(ctx, TechnicalMaintenanceRequest{Kind: check.kind, Scope: scope, RecordID: check.id, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Mode: ConversationMaintenanceDryRun})
		if err != nil || result == nil || !result.Eligible {
			t.Fatalf("exact MySQL candidate kind=%s result=%#v err=%v", check.kind, result, err)
		}
		// A non-empty cursor exercises the generated reader's paginated SQL.
		// Exact-ID reads above cannot catch parser failures on the next page.
		cursor := fmt.Sprintf("%04d%s%s%s%s", check.priority, technicalMaintenanceCursorSeparator, check.kind, technicalMaintenanceCursorSeparator, check.id)
		if _, err = svc.ListTechnicalMaintenanceCandidates(ctx, TechnicalMaintenanceCandidateRequest{
			Scope: scope, OlderThan: cutoff, EvaluatedAt: evaluatedAt, AfterCursor: cursor, Limit: 1,
		}); err != nil {
			t.Fatalf("paginated MySQL candidates kind=%s: %v", check.kind, err)
		}
	}
	recent, err := svc.MaintainTechnicalCandidate(ctx, TechnicalMaintenanceRequest{Kind: TechnicalMaintenanceSession, Scope: TechnicalMaintenanceUnclassified, RecordID: recentSessionID, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Mode: ConversationMaintenanceDryRun})
	if err != nil || recent == nil || recent.Eligible {
		t.Fatalf("recent MySQL session result=%#v err=%v", recent, err)
	}

	dryRun, err := svc.MaintainTechnicalCandidate(ctx, TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceReportRun, Scope: TechnicalMaintenanceInteractive,
		RecordID: runID, OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDryRun,
	})
	if err != nil || dryRun == nil || !dryRun.Eligible || dryRun.Deleted {
		t.Fatalf("MySQL technical dry-run result=%#v err=%v", dryRun, err)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", runID, 1)

	lease := acquireTestMaintenanceLease(t, svc, leaseKey, "worker-"+suffix)
	deleted, err := svc.MaintainTechnicalCandidate(ctx, TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceReportRun, Scope: TechnicalMaintenanceInteractive,
		RecordID: runID, OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: lease,
	})
	if err != nil {
		t.Fatalf("delete MySQL technical report state: %v", err)
	}
	if deleted == nil || !deleted.Deleted || deleted.DeletedRows != 4 {
		t.Fatalf("delete MySQL technical report result = %#v", deleted)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", runID, 0)
	assertTechnicalRowCount(t, db, "report_export_job", "job_id", jobID, 0)
	assertTechnicalRowCount(t, db, "report_export_artifact", "artifact_id", artifactID, 0)
	assertTechnicalRowCount(t, db, "report_audit_event", "event_id", auditID, 0)
	assertTechnicalRowCount(t, db, "report_shared_artifact", "artifact_id", sharedID, 1)

	sessionResult, err := svc.MaintainTechnicalCandidate(ctx, TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceSession, Scope: TechnicalMaintenanceUnclassified,
		RecordID: sessionID, OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: lease,
	})
	if err != nil || sessionResult == nil || !sessionResult.Deleted {
		t.Fatalf("delete expired MySQL session result=%#v err=%v", sessionResult, err)
	}
	assertTechnicalRowCount(t, db, "session", "id", sessionID, 0)
	assertTechnicalRowCount(t, db, "session", "id", recentSessionID, 1)
}
