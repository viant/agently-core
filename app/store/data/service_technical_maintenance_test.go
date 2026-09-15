package data

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestTechnicalMaintenance_SQLiteDeletesOldTechnicalStateAndPreservesSavedReports(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cutoff := evaluatedAt.Add(-30 * 24 * time.Hour)
	old := evaluatedAt.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
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

	candidates := listAllTechnicalCandidates(t, svc, TechnicalMaintenanceCandidateRequest{
		Scope: TechnicalMaintenanceInteractive, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 1,
	})
	wantKinds := []TechnicalMaintenanceKind{
		TechnicalMaintenanceReportRun,
		TechnicalMaintenanceReportExportJob,
		TechnicalMaintenanceReportAudit,
	}
	gotKinds := make([]TechnicalMaintenanceKind, 0, len(candidates))
	for _, candidate := range candidates {
		gotKinds = append(gotKinds, candidate.Kind)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("candidate kinds = %v, want %v; candidates=%#v", gotKinds, wantKinds, candidates)
	}

	dryRun, err := svc.MaintainTechnicalCandidate(context.Background(), TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceReportRun, Scope: TechnicalMaintenanceInteractive,
		RecordID: "technical-run", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDryRun,
	})
	if err != nil {
		t.Fatalf("dry-run technical maintenance: %v", err)
	}
	if !dryRun.Eligible || dryRun.Deleted || dryRun.DeletedRows != 0 || dryRun.Reason != TechnicalMaintenanceEligibleReason {
		t.Fatalf("dry-run result = %#v", dryRun)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", "technical-run", 1)

	deleted, err := svc.MaintainTechnicalCandidate(context.Background(), TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceReportRun, Scope: TechnicalMaintenanceInteractive,
		RecordID: "technical-run", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, "technical-delete", "worker"),
	})
	if err != nil {
		t.Fatalf("delete technical maintenance: %v", err)
	}
	if !deleted.Eligible || !deleted.Deleted || deleted.DeletedRows != 4 || deleted.Reason != TechnicalMaintenanceDeletedReason {
		t.Fatalf("delete result = %#v", deleted)
	}
	assertTechnicalRowCount(t, db, "report_run", "report_run_id", "technical-run", 0)
	assertTechnicalRowCount(t, db, "report_export_job", "job_id", "technical-job", 0)
	assertTechnicalRowCount(t, db, "report_export_artifact", "artifact_id", "technical-artifact", 0)
	assertTechnicalRowCount(t, db, "report_audit_event", "event_id", "technical-audit", 0)
	assertTechnicalRowCount(t, db, "report_shared_artifact", "artifact_id", "saved-report", 1)
}

func TestTechnicalMaintenance_SQLiteScopesTTLSessionAndTransactionalRecheck(t *testing.T) {
	evaluatedAt := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	cutoff := evaluatedAt.Add(-30 * 24 * time.Hour)
	oldTime := evaluatedAt.Add(-60 * 24 * time.Hour)
	old := oldTime.Format(time.RFC3339)
	recent := evaluatedAt.Add(-time.Hour).Format(time.RFC3339)
	recentlyExpired := evaluatedAt.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	svc, db := newSeededServiceWithDB(t, func(t *testing.T, db *sql.DB) {
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

	scheduled := listAllTechnicalCandidates(t, svc, TechnicalMaintenanceCandidateRequest{
		Scope: TechnicalMaintenanceScheduled, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 10,
	})
	if len(scheduled) != 1 || scheduled[0].Kind != TechnicalMaintenanceReportRun || scheduled[0].RecordID != "scheduled-run" {
		t.Fatalf("scheduled candidates = %#v", scheduled)
	}

	unclassified := listAllTechnicalCandidates(t, svc, TechnicalMaintenanceCandidateRequest{
		Scope: TechnicalMaintenanceUnclassified, OlderThan: cutoff, EvaluatedAt: evaluatedAt, Limit: 20,
	})
	found := map[string]TechnicalMaintenanceKind{}
	for _, candidate := range unclassified {
		found[candidate.RecordID] = candidate.Kind
	}
	want := map[string]TechnicalMaintenanceKind{
		"unclassified-run": TechnicalMaintenanceReportRun,
		"ttl-expired-job":  TechnicalMaintenanceReportExportJob,
		"audit-recheck":    TechnicalMaintenanceReportAudit,
		"expired-session":  TechnicalMaintenanceSession,
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
	rechecked, err := svc.MaintainTechnicalCandidate(context.Background(), TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceReportAudit, Scope: TechnicalMaintenanceUnclassified,
		RecordID: "audit-recheck", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, "technical-recheck", "worker"),
	})
	if err != nil {
		t.Fatalf("recheck technical maintenance: %v", err)
	}
	if rechecked.Eligible || rechecked.Deleted || rechecked.Reason != TechnicalMaintenanceNoLongerEligibleReason {
		t.Fatalf("recheck result = %#v", rechecked)
	}
	assertTechnicalRowCount(t, db, "report_audit_event", "event_id", "audit-recheck", 1)

	// A session that was old enough during selection must be preserved if its
	// expiry is moved inside the retention window before the mutation starts.
	mustExecTechnical(t, db, `UPDATE session SET expires_at = ? WHERE id = ?`, recentlyExpired, "expired-session")
	sessionRechecked, err := svc.MaintainTechnicalCandidate(context.Background(), TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceSession, Scope: TechnicalMaintenanceUnclassified,
		RecordID: "expired-session", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, "technical-session-recheck", "worker"),
	})
	if err != nil {
		t.Fatalf("recheck expired session maintenance: %v", err)
	}
	if sessionRechecked.Eligible || sessionRechecked.Deleted || sessionRechecked.Reason != TechnicalMaintenanceNoLongerEligibleReason {
		t.Fatalf("rechecked session result = %#v", sessionRechecked)
	}
	assertTechnicalRowCount(t, db, "session", "id", "expired-session", 1)
	mustExecTechnical(t, db, `UPDATE session SET expires_at = ? WHERE id = ?`, old, "expired-session")

	sessionResult, err := svc.MaintainTechnicalCandidate(context.Background(), TechnicalMaintenanceRequest{
		Kind: TechnicalMaintenanceSession, Scope: TechnicalMaintenanceUnclassified,
		RecordID: "expired-session", OlderThan: cutoff, EvaluatedAt: evaluatedAt,
		Mode: ConversationMaintenanceDelete, Lease: acquireTestMaintenanceLease(t, svc, "technical-session", "worker"),
	})
	if err != nil || sessionResult == nil || !sessionResult.Deleted {
		t.Fatalf("expired session result=%#v err=%v", sessionResult, err)
	}
	assertTechnicalRowCount(t, db, "session", "id", "expired-session", 0)
	assertTechnicalRowCount(t, db, "session", "id", "recently-expired-session", 1)
	assertTechnicalRowCount(t, db, "session", "id", "live-session", 1)
}

func listAllTechnicalCandidates(t *testing.T, svc Service, request TechnicalMaintenanceCandidateRequest) []TechnicalMaintenanceCandidate {
	t.Helper()
	var result []TechnicalMaintenanceCandidate
	for {
		page, err := svc.ListTechnicalMaintenanceCandidates(context.Background(), request)
		if err != nil {
			t.Fatalf("ListTechnicalMaintenanceCandidates(%q): %v", request.AfterCursor, err)
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
