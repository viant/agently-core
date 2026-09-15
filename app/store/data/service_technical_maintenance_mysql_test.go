package data

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
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

	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New(): %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "mysql", dsn)); err != nil {
		t.Fatalf("AddConnectors(): %v", err)
	}
	if err = registerReadComponents(ctx, dao); err != nil {
		t.Fatalf("registerReadComponents(): %v", err)
	}
	svc := NewService(dao)

	// Exercise every MySQL candidate query against exact fixture IDs. This is
	// deterministic even when the developer database contains unrelated rows.
	checks := []struct {
		kind TechnicalMaintenanceKind
		id   string
	}{
		{TechnicalMaintenanceReportRun, runID},
		{TechnicalMaintenanceReportExportJob, jobID},
		{TechnicalMaintenanceReportAudit, auditID},
		{TechnicalMaintenanceSession, sessionID},
	}
	for _, check := range checks {
		scope := TechnicalMaintenanceInteractive
		if check.kind == TechnicalMaintenanceSession {
			scope = TechnicalMaintenanceUnclassified
		}
		request := TechnicalMaintenanceCandidateRequest{Scope: scope, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 1}
		page, listErr := listTechnicalMaintenanceRuleCandidates(ctx, db, "mysql", technicalMaintenanceRuleByKind(check.kind), request, "", check.id, 1)
		if listErr != nil {
			t.Fatalf("list exact MySQL technical candidate kind=%s: %v", check.kind, listErr)
		}
		if len(page) != 1 || page[0].RecordID != check.id {
			t.Fatalf("exact MySQL candidate kind=%s = %#v, want %q", check.kind, page, check.id)
		}
	}
	recentPage, err := listTechnicalMaintenanceRuleCandidates(ctx, db, "mysql", technicalMaintenanceRuleByKind(TechnicalMaintenanceSession), TechnicalMaintenanceCandidateRequest{
		Scope: TechnicalMaintenanceUnclassified, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 1,
	}, "", recentSessionID, 1)
	if err != nil {
		t.Fatalf("list recently expired MySQL session candidate: %v", err)
	}
	if len(recentPage) != 0 {
		t.Fatalf("session expired within retention was selected: %#v", recentPage)
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
