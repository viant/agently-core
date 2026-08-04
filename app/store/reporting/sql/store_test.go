package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/viant/agently-core/app/store/data"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	reportmemory "github.com/viant/agently-core/app/store/reporting/memory"
	"github.com/viant/agently-core/internal/testutil/dbtest"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	authsvc "github.com/viant/agently-core/service/auth"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/datly"
	"github.com/viant/datly/view"
)

func TestStore_NewDoesNotProvisionReportingSchema(t *testing.T) {
	ctx := context.Background()
	db, dbPath, cleanup := dbtest.CreateTempSQLiteDB(t, "reporting-store-empty-schema")
	t.Cleanup(cleanup)

	dao, err := datly.New(ctx)
	if err != nil {
		t.Fatalf("datly.New() error = %v", err)
	}
	if err = dao.AddConnectors(ctx, view.NewConnector("agently", "sqlite", dbPath)); err != nil {
		t.Fatalf("AddConnectors() error = %v", err)
	}
	isolateReportingComponents(t, dao, "agently")

	if _, err = New(ctx, dao, "agently", nil, nil); err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var tableCount int
	if err = db.QueryRowContext(ctx, `
SELECT COUNT(1)
FROM sqlite_master
WHERE type = 'table' AND name GLOB 'report_*'`).Scan(&tableCount); err != nil {
		t.Fatalf("query reporting tables error = %v", err)
	}
	if tableCount != 0 {
		t.Fatalf("reporting table count = %d, want 0", tableCount)
	}
}

func TestStore_SharedArtifactRoundTripIsScopedByOwner(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "owner-1")
	otherCtx := authsvc.InjectUser(ctx, "owner-2")
	createdAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	artifact := &reportshareartifact.Record{
		ArtifactID:      "shared-1",
		ArtifactRef:     "reportBuilder.savedReportPayload://forecasting_q3",
		OwnerID:         "owner-1",
		OwnerRef:        "user://owner-1",
		Kind:            "reportBuilder.savedReportPayload",
		Lifecycle:       "draft",
		Version:         1,
		ReportID:        "forecasting_q3",
		Title:           "Forecasting Q3",
		DocumentVersion: 7,
		Document:        []byte(`{"kind":"reportDocument","id":"forecasting_q3"}`),
		ReportSpec:      []byte(`{"kind":"reportSpec","version":1}`),
		CompileState:    []byte(`{"status":"clean"}`),
		Metadata:        []byte(`{"workspace":"steward"}`),
		CreatedAt:       createdAt,
	}

	if err := client.CreateSharedArtifact(ownerCtx, artifact); err != nil {
		t.Fatalf("CreateSharedArtifact() error = %v", err)
	}

	got, err := client.GetSharedArtifact(ownerCtx, "shared-1")
	if err != nil {
		t.Fatalf("GetSharedArtifact(owner) error = %v", err)
	}
	if got == nil || got.OwnerID != "owner-1" || got.Title != "Forecasting Q3" {
		t.Fatalf("GetSharedArtifact(owner) = %+v, want persisted owner row", got)
	}

	if _, err := client.GetSharedArtifact(otherCtx, "shared-1"); err == nil {
		t.Fatalf("GetSharedArtifact(other) expected not found")
	}

	listed, err := client.ListSharedArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts(owner) error = %v", err)
	}
	if len(listed) != 1 || listed[0] == nil || listed[0].ArtifactID != "shared-1" {
		t.Fatalf("ListSharedArtifacts(owner) = %+v, want one owned artifact", listed)
	}

	otherListed, err := client.ListSharedArtifacts(otherCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts(other) error = %v", err)
	}
	if len(otherListed) != 0 {
		t.Fatalf("ListSharedArtifacts(other) = %+v, want empty", otherListed)
	}

	updatedAt := createdAt.Add(2 * time.Hour)
	got.Title = "Forecasting Q3 Updated"
	got.Version = 2
	got.UpdatedAt = &updatedAt
	if err := client.UpdateSharedArtifact(ownerCtx, got); err != nil {
		t.Fatalf("UpdateSharedArtifact(owner) error = %v", err)
	}

	afterUpdate, err := client.GetSharedArtifact(ownerCtx, "shared-1")
	if err != nil {
		t.Fatalf("GetSharedArtifact(after update) error = %v", err)
	}
	if afterUpdate == nil || afterUpdate.Title != "Forecasting Q3 Updated" || afterUpdate.Version != 2 {
		t.Fatalf("GetSharedArtifact(after update) = %+v, want updated row", afterUpdate)
	}

	got.OwnerID = "owner-2"
	got.OwnerRef = "user://owner-2"
	if err := client.UpdateSharedArtifact(otherCtx, got); err == nil {
		t.Fatalf("UpdateSharedArtifact(other) expected not found")
	}
}

func TestStore_ReportRunAndContextCASParity(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runStore, ok := client.(reportstore.RunClient)
	if !ok {
		t.Fatalf("SQL reporting store does not implement RunClient")
	}
	store := client.(*Store)
	db, err := store.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	assertExactT2JobColumns(t, db)
	if _, err = db.ExecContext(ctx, `
INSERT INTO conversation (id, created_by_user_id, visibility, shareable)
VALUES (?, ?, 'private', 0)`, "conv-run-1", "owner-run-1"); err != nil {
		t.Fatalf("insert conversation error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "owner-run-1")
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	run := &reportrun.Record{
		ReportRunID:     "run-sql-1",
		OwnerID:         "owner-run-1",
		ConversationID:  "conv-run-1",
		Materializer:    reportrun.MaterializerLegacyBrowser,
		Status:          reportrun.StatusRunning,
		Revision:        1,
		UIRunRequestID:  "ui-request-sql-1",
		RequestedParams: []byte(`{"orderId":2676946}`),
		StartedAt:       now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := runStore.CreateReportRun(ownerCtx, run); err != nil {
		t.Fatalf("CreateReportRun() error = %v", err)
	}
	byRequest, err := runStore.GetReportRunByRequestID(ownerCtx, "ui-request-sql-1")
	if err != nil || byRequest.ReportRunID != "run-sql-1" {
		t.Fatalf("GetReportRunByRequestID() = %+v, %v", byRequest, err)
	}
	run.Status = reportrun.StatusCompleted
	run.Revision = 2
	run.ReportSpec = []byte(`{"kind":"reportSpec"}`)
	run.ReportFill = []byte(`{"kind":"reportFill"}`)
	run.ReportPrint = []byte(`{"kind":"reportPrint"}`)
	completedAt := now.Add(time.Second)
	run.CompletedAt = &completedAt
	run.UpdatedAt = completedAt
	if err := runStore.UpdateReportRunCAS(ownerCtx, run, 1); err != nil {
		t.Fatalf("UpdateReportRunCAS() error = %v", err)
	}
	if err := runStore.UpdateReportRunCAS(ownerCtx, run, 1); !errors.Is(err, reportstore.ErrCASMismatch) {
		t.Fatalf("UpdateReportRunCAS(stale) error = %v, want CAS", err)
	}
	pointer := &reportcontext.Record{
		OwnerID:           "owner-run-1",
		ConversationID:    "conv-run-1",
		ActiveReportRunID: "run-sql-1",
		Revision:          1,
		ActivationSource:  "prompt",
		ActorID:           "owner-run-1",
		UpdatedAt:         completedAt,
	}
	if err := runStore.PutConversationReportContextCAS(ownerCtx, pointer, 0); err != nil {
		t.Fatalf("PutConversationReportContextCAS() error = %v", err)
	}
	if err := runStore.PutConversationReportContextCAS(ownerCtx, pointer, 0); !errors.Is(err, reportstore.ErrCASMismatch) {
		t.Fatalf("PutConversationReportContextCAS(stale) error = %v, want CAS", err)
	}
	otherCtx := authsvc.InjectUser(ctx, "owner-run-2")
	if _, err := runStore.GetReportRun(otherCtx, "run-sql-1"); !errors.Is(err, reportstore.ErrNotFound) {
		t.Fatalf("GetReportRun(other owner) error = %v, want not found", err)
	}
}

func TestStore_RunExportCopiesCompletedSnapshotAndGuardsLifecycle(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runStore := client.(reportstore.RunClient)
	exportStore := client.(reportstore.RunExportClient)
	store := client.(*Store)
	db, err := store.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	if _, err = db.ExecContext(ctx, `
INSERT INTO conversation (id, created_by_user_id, visibility, shareable)
VALUES (?, ?, 'private', 0)`, "conv-export-sql", "owner-export-sql"); err != nil {
		t.Fatalf("insert conversation error = %v", err)
	}
	ownerCtx := authsvc.InjectUser(ctx, "owner-export-sql")
	now := time.Date(2026, 7, 29, 18, 0, 0, 0, time.UTC)
	run := completedSQLRun("run-export-sql", "owner-export-sql", "conv-export-sql", now)
	run.Revision = 13
	run.ReportSpec = []byte(`{"kind":"authoritativeSpec"}`)
	run.ReportFill = []byte(`{"kind":"authoritativeFill"}`)
	run.ReportPrint = []byte(`{"kind":"authoritativePrint"}`)
	if err := runStore.CreateReportRun(ownerCtx, run); err != nil {
		t.Fatalf("CreateReportRun() error = %v", err)
	}
	candidate := &reportjob.Record{
		JobID:           "job-export-sql",
		ArtifactRef:     "report-run://run-export-sql",
		OwnerID:         "owner-export-sql",
		ConversationID:  "conv-export-sql",
		Format:          "pdf",
		Scope:           "draft",
		Status:          "queued",
		ReportRunID:     run.ReportRunID,
		ExportRequestID: "request-export-sql",
		ReportSpec:      []byte(`{"kind":"untrustedBrowserSpec"}`),
		SubmittedAt:     now,
	}
	job, replay, err := exportStore.SubmitJobFromRun(ownerCtx, candidate)
	if err != nil {
		t.Fatalf("SubmitJobFromRun() error = %v", err)
	}
	if replay {
		t.Fatalf("SubmitJobFromRun() replay = true, want false")
	}
	if job.ReportRunRevision != run.Revision ||
		string(job.ReportSpec) != string(run.ReportSpec) ||
		string(job.ReportFill) != string(run.ReportFill) ||
		string(job.ReportPrint) != string(run.ReportPrint) {
		t.Fatalf("SubmitJobFromRun() did not copy authoritative run: %+v", job)
	}
	unguarded := *job
	unguarded.Status = "succeeded"
	if err := client.UpdateJob(ownerCtx, &unguarded); !errors.Is(err, reportstore.ErrInvalidTransition) {
		t.Fatalf("UpdateJob(run-reference) error = %v, want guarded transition rejection", err)
	}
	replayed, replay, err := exportStore.SubmitJobFromRun(ownerCtx, candidate)
	if err != nil || !replay || replayed.JobID != job.JobID {
		t.Fatalf("SubmitJobFromRun(replay) = %+v, %t, %v", replayed, replay, err)
	}
	conflict := *candidate
	conflict.ReportRunID = "different-run"
	conflict.ArtifactRef = "report-run://different-run"
	if _, _, err := exportStore.SubmitJobFromRun(ownerCtx, &conflict); !errors.Is(err, reportstore.ErrConflict) {
		t.Fatalf("SubmitJobFromRun(conflict) error = %v, want conflict", err)
	}
	formatConflict := *candidate
	formatConflict.Format = "xlsx"
	if _, _, err := exportStore.SubmitJobFromRun(ownerCtx, &formatConflict); !errors.Is(err, reportstore.ErrConflict) {
		t.Fatalf("SubmitJobFromRun(format conflict) error = %v, want conflict", err)
	}
	optionsConflict := *candidate
	optionsConflict.Scope = "saved_payload"
	if _, _, err := exportStore.SubmitJobFromRun(ownerCtx, &optionsConflict); !errors.Is(err, reportstore.ErrConflict) {
		t.Fatalf("SubmitJobFromRun(options conflict) error = %v, want conflict", err)
	}
	running, err := exportStore.ClaimJob(ownerCtx, job.JobID, now.Add(time.Second))
	if err != nil || running.Status != "running" {
		t.Fatalf("ClaimJob() = %+v, %v", running, err)
	}
	if _, err := exportStore.ClaimJob(ownerCtx, job.JobID, now.Add(2*time.Second)); !errors.Is(err, reportstore.ErrInvalidTransition) {
		t.Fatalf("ClaimJob(duplicate) error = %v, want invalid transition", err)
	}
	completed, err := exportStore.CompleteJobWithArtifact(ownerCtx, job.JobID, &reportartifact.Record{
		ArtifactID:  "artifact-export-sql",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-sql"),
		CreatedAt:   now.Add(2 * time.Second),
	}, nil, now.Add(2*time.Second), time.Hour)
	if err != nil || completed.Status != "succeeded" || completed.ArtifactID != "artifact-export-sql" {
		t.Fatalf("CompleteJobWithArtifact() = %+v, %v", completed, err)
	}
	duplicate, err := exportStore.CompleteJobWithArtifact(ownerCtx, job.JobID, &reportartifact.Record{
		ArtifactID: "ignored-retry-artifact",
	}, nil, now.Add(3*time.Second), time.Hour)
	if err != nil || duplicate.ArtifactID != completed.ArtifactID {
		t.Fatalf("CompleteJobWithArtifact(replay) = %+v, %v", duplicate, err)
	}
	artifacts, err := client.ListArtifacts(ownerCtx)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("ListArtifacts() = %+v, %v, want exactly one", artifacts, err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO report_export_artifact (
  artifact_id, job_id, artifact_ref, owner_id, format, content_type, inline_data, created_at, retention_ttl_sec
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"artifact-export-sql-duplicate", job.JobID, job.ArtifactRef, job.OwnerID,
		job.Format, "application/pdf", []byte("%PDF-duplicate"), now.Add(4*time.Second), 0,
	); err == nil {
		t.Fatal("second artifact for one job unexpectedly bypassed UNIQUE(job_id)")
	}
}

func TestStore_RunExportKeepsLegacySchemaReadable(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := client.(*Store)
	db, err := store.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE report_export_artifact`,
		`DROP TABLE report_export_job`,
		`CREATE TABLE report_export_job (
			job_id TEXT PRIMARY KEY,
			artifact_ref TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			conversation_id TEXT,
			workspace_id TEXT,
			auth_context_ref TEXT,
			format TEXT NOT NULL,
			scope TEXT NOT NULL,
			status TEXT NOT NULL,
			report_spec_json BLOB,
			report_fill_json BLOB,
			report_print_json BLOB,
			metadata_json BLOB,
			artifact_id TEXT,
			error_text TEXT,
			diagnostics_json BLOB,
			submitted_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			retention_ttl_sec INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE report_export_artifact (
			artifact_id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			artifact_ref TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			format TEXT NOT NULL,
			content_type TEXT NOT NULL,
			inline_data BLOB,
			created_at DATETIME NOT NULL,
			retention_ttl_sec INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("legacy schema statement error = %v: %s", err, statement)
		}
	}
	ownerCtx := authsvc.InjectUser(ctx, "legacy-owner")
	now := time.Date(2026, 7, 29, 19, 0, 0, 0, time.UTC)
	legacy := &reportjob.Record{
		JobID:       "legacy-job",
		ArtifactRef: "legacy://report",
		OwnerID:     "legacy-owner",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}
	if err := client.CreateJob(ownerCtx, legacy); err != nil {
		t.Fatalf("CreateJob(legacy) error = %v", err)
	}
	got, err := client.GetJob(ownerCtx, legacy.JobID)
	if err != nil || got.ReportRunID != "" || got.ExportRequestID != "" {
		t.Fatalf("GetJob(legacy) = %+v, %v", got, err)
	}
	exportStore := client.(reportstore.RunExportClient)
	running, err := exportStore.ClaimJob(ownerCtx, legacy.JobID, now.Add(time.Second))
	if err != nil || running.Status != "running" {
		t.Fatalf("ClaimJob(legacy) = %+v, %v", running, err)
	}
	completed, err := exportStore.CompleteJobWithArtifact(ownerCtx, legacy.JobID, &reportartifact.Record{
		ArtifactID:  "legacy-artifact",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-legacy"),
		CreatedAt:   now.Add(2 * time.Second),
	}, nil, now.Add(2*time.Second), 0)
	if err != nil || completed.Status != "succeeded" {
		t.Fatalf("CompleteJobWithArtifact(legacy) = %+v, %v", completed, err)
	}
}

func TestStore_RunExportUsesT2ColumnsWithoutRuntimeConstraintInspection(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store := client.(*Store)
	db, err := store.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	for _, statement := range []string{
		`DROP TABLE report_export_artifact`,
		`DROP TABLE report_export_job`,
		`CREATE TABLE report_export_job (
			job_id TEXT PRIMARY KEY,
			artifact_ref TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			conversation_id TEXT,
			workspace_id TEXT,
			auth_context_ref TEXT,
			format TEXT NOT NULL,
			scope TEXT NOT NULL,
			status TEXT NOT NULL,
			report_run_id TEXT,
			report_run_revision INTEGER,
			export_request_id TEXT,
			report_spec_json BLOB,
			report_fill_json BLOB,
			report_print_json BLOB,
			metadata_json BLOB,
			artifact_id TEXT,
			error_text TEXT,
			diagnostics_json BLOB,
			submitted_at DATETIME NOT NULL,
			started_at DATETIME,
			completed_at DATETIME,
			retention_ttl_sec INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE report_export_artifact (
			artifact_id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			artifact_ref TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			format TEXT NOT NULL,
			content_type TEXT NOT NULL,
			inline_data BLOB,
			created_at DATETIME NOT NULL,
			retention_ttl_sec INTEGER NOT NULL DEFAULT 0
		)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("partial T2 schema statement error = %v: %s", err, statement)
		}
	}
	columnsPresent, err := hasColumns(
		ctx, db, "report_export_job", "report_run_id", "report_run_revision", "export_request_id",
	)
	if err != nil || !columnsPresent {
		t.Fatalf("partial T2 schema columns present = %t, error = %v, want all three columns", columnsPresent, err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "partial-t2-owner")
	now := time.Date(2026, 7, 29, 19, 30, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
INSERT INTO conversation (id, created_by_user_id, visibility, shareable)
VALUES (?, ?, 'private', 0)`, "partial-t2-conversation", "partial-t2-owner"); err != nil {
		t.Fatalf("insert conversation error = %v", err)
	}
	runStore := client.(reportstore.RunClient)
	run := completedSQLRun("partial-t2-run", "partial-t2-owner", "partial-t2-conversation", now)
	if err := runStore.CreateReportRun(ownerCtx, run); err != nil {
		t.Fatalf("CreateReportRun(partial T2 schema) error = %v", err)
	}
	legacy := &reportjob.Record{
		JobID:       "partial-t2-legacy-job",
		ArtifactRef: "legacy://partial-t2",
		OwnerID:     "partial-t2-owner",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}
	if err := client.CreateJob(ownerCtx, legacy); err != nil {
		t.Fatalf("CreateJob(partial T2 schema) error = %v", err)
	}
	if _, err := client.GetJob(ownerCtx, legacy.JobID); err != nil {
		t.Fatalf("GetJob(partial T2 schema) error = %v", err)
	}
	exportStore := client.(reportstore.RunExportClient)
	running, err := exportStore.ClaimJob(ownerCtx, legacy.JobID, now.Add(time.Second))
	if err != nil || running.Status != "running" {
		t.Fatalf("ClaimJob(partial T2 schema) = %+v, %v", running, err)
	}
	if _, err := exportStore.FailJob(ownerCtx, legacy.JobID, "expected", nil, now.Add(2*time.Second)); err != nil {
		t.Fatalf("FailJob(partial T2 schema) error = %v", err)
	}

	job, replay, err := exportStore.SubmitJobFromRun(ownerCtx, &reportjob.Record{
		JobID:           "partial-t2-run-job",
		ArtifactRef:     "report-run://partial-t2-run",
		OwnerID:         "partial-t2-owner",
		ConversationID:  "partial-t2-conversation",
		Format:          "pdf",
		Scope:           "draft",
		Status:          "queued",
		ReportRunID:     "partial-t2-run",
		ExportRequestID: "partial-t2-request",
		SubmittedAt:     now,
	})
	if err != nil || replay || job == nil || job.ReportRunID != run.ReportRunID || job.ReportRunRevision != run.Revision {
		t.Fatalf("SubmitJobFromRun(partial T2 schema) = %+v, %t, %v", job, replay, err)
	}
}

func TestStore_AdoptionUsesOneTransactionAndRejectsCompletedSnapshotMutation(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runStore := client.(reportstore.RunClient)
	store := client.(*Store)
	db, err := store.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	for _, conversationID := range []string{"conv-stale", "conv-concurrent"} {
		if _, err := db.ExecContext(ctx, `
INSERT INTO conversation (id, created_by_user_id, visibility, shareable)
VALUES (?, ?, 'private', 0)`, conversationID, "owner-adopt"); err != nil {
			t.Fatalf("insert conversation %s error = %v", conversationID, err)
		}
	}
	ownerCtx := authsvc.InjectUser(ctx, "owner-adopt")
	now := time.Date(2026, 7, 29, 15, 30, 0, 0, time.UTC)

	prior := completedSQLRun("prior-run", "owner-adopt", "conv-stale", now)
	if err := runStore.CreateReportRun(ownerCtx, prior); err != nil {
		t.Fatalf("CreateReportRun(prior) error = %v", err)
	}
	if err := runStore.PutConversationReportContextCAS(ownerCtx, &reportcontext.Record{
		OwnerID:           "owner-adopt",
		ConversationID:    "conv-stale",
		ActiveReportRunID: prior.ReportRunID,
		Revision:          1,
		ActivationSource:  "manual",
		ActorID:           "owner-adopt",
		UpdatedAt:         now,
	}, 0); err != nil {
		t.Fatalf("PutConversationReportContextCAS(prior) error = %v", err)
	}
	manual := completedSQLRun("manual-run", "owner-adopt", "", now)
	manual.Origin = "manual"
	if err := runStore.CreateReportRun(ownerCtx, manual); err != nil {
		t.Fatalf("CreateReportRun(manual) error = %v", err)
	}
	staleRun := adoptedSQLRun(manual, "conv-stale", now.Add(time.Second))
	stalePointer := adoptedSQLContext(staleRun, 1, now.Add(time.Second))
	if err := runStore.AdoptReportRunAndContextCAS(ownerCtx, staleRun, 2, stalePointer, 0); !errors.Is(err, reportstore.ErrCASMismatch) {
		t.Fatalf("AdoptReportRunAndContextCAS(stale) error = %v, want CAS", err)
	}
	unchangedRun, err := runStore.GetReportRun(ownerCtx, manual.ReportRunID)
	if err != nil || unchangedRun.ConversationID != "" {
		t.Fatalf("stale adoption changed run: %+v, %v", unchangedRun, err)
	}
	unchangedPointer, err := runStore.GetConversationReportContext(ownerCtx, "conv-stale")
	if err != nil || unchangedPointer.ActiveReportRunID != prior.ReportRunID {
		t.Fatalf("stale adoption changed pointer: %+v, %v", unchangedPointer, err)
	}

	mutated := *manual
	mutated.ReportFill = []byte(`{"kind":"changed"}`)
	mutated.Revision++
	if err := runStore.UpdateReportRunCAS(ownerCtx, &mutated, manual.Revision); !errors.Is(err, reportstore.ErrImmutable) {
		t.Fatalf("UpdateReportRunCAS(completed mutation) error = %v, want immutable", err)
	}

	concurrent := completedSQLRun("manual-concurrent", "owner-adopt", "", now)
	concurrent.Origin = "manual"
	if err := runStore.CreateReportRun(ownerCtx, concurrent); err != nil {
		t.Fatalf("CreateReportRun(concurrent) error = %v", err)
	}
	next := adoptedSQLRun(concurrent, "conv-concurrent", now.Add(2*time.Second))
	nextPointer := adoptedSQLContext(next, 0, now.Add(2*time.Second))
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- runStore.AdoptReportRunAndContextCAS(ownerCtx, next, 2, nextPointer, 0)
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	casFailures := 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, reportstore.ErrCASMismatch):
			casFailures++
		default:
			t.Fatalf("concurrent SQL adoption error = %v", result)
		}
	}
	if successes != 1 || casFailures != 1 {
		t.Fatalf("concurrent SQL adoption successes=%d casFailures=%d", successes, casFailures)
	}
	gotRun, err := runStore.GetReportRun(ownerCtx, concurrent.ReportRunID)
	if err != nil || gotRun.ConversationID != "conv-concurrent" || gotRun.Revision != 3 {
		t.Fatalf("GetReportRun(after adoption) = %+v, %v", gotRun, err)
	}
	gotPointer, err := runStore.GetConversationReportContext(ownerCtx, "conv-concurrent")
	if err != nil || gotPointer.ActiveReportRunID != concurrent.ReportRunID || gotPointer.Revision != 1 {
		t.Fatalf("GetConversationReportContext(after adoption) = %+v, %v", gotPointer, err)
	}
}

func completedSQLRun(id, ownerID, conversationID string, now time.Time) *reportrun.Record {
	completedAt := now
	return &reportrun.Record{
		ReportRunID:    id,
		OwnerID:        ownerID,
		ConversationID: conversationID,
		Materializer:   reportrun.MaterializerLegacyBrowser,
		Origin:         "prompt",
		Status:         reportrun.StatusCompleted,
		Revision:       2,
		UIRunRequestID: "request-" + id,
		ReportSpec:     []byte(`{"kind":"reportSpec"}`),
		ReportFill:     []byte(`{"kind":"reportFill"}`),
		ReportPrint:    []byte(`{"kind":"reportPrint"}`),
		StartedAt:      now,
		CompletedAt:    &completedAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func adoptedSQLRun(current *reportrun.Record, conversationID string, now time.Time) *reportrun.Record {
	next := *current
	next.ConversationID = conversationID
	next.AdoptionSource = "adopt"
	next.ActorID = current.OwnerID
	next.Revision++
	next.UpdatedAt = now
	return &next
}

func adoptedSQLContext(run *reportrun.Record, expectedRevision int64, now time.Time) *reportcontext.Record {
	return &reportcontext.Record{
		OwnerID:           run.OwnerID,
		ConversationID:    run.ConversationID,
		ActiveReportRunID: run.ReportRunID,
		Revision:          expectedRevision + 1,
		ActivationSource:  "adopt",
		ActorID:           run.OwnerID,
		UpdatedAt:         now,
	}
}

func TestStore_ImportsFilesystemReportingState(t *testing.T) {
	root := t.TempDir()
	stateStore := fsstate.NewStateStore(root)
	fsClient := reportfs.New(stateStore)
	fsAudit := reportfs.NewAuditSink(stateStore)
	now := time.Date(2026, 7, 13, 18, 0, 0, 0, time.UTC)

	mustNoErr(t, fsClient.CreateJob(context.Background(), &reportjob.Record{
		JobID:       "job-fs-1",
		ArtifactRef: "report://draft/fs",
		OwnerID:     "user-fs",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}))
	mustNoErr(t, fsClient.PutArtifact(context.Background(), &reportartifact.Record{
		ArtifactID:  "artifact-fs-1",
		JobID:       "job-fs-1",
		ArtifactRef: "report://draft/fs",
		OwnerID:     "user-fs",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-fs"),
		CreatedAt:   now,
	}))
	mustNoErr(t, fsClient.CreateSharedArtifact(context.Background(), &reportshareartifact.Record{
		ArtifactID:      "shared-fs-1",
		ArtifactRef:     "reportBuilder.savedReportPayload://fs_report",
		OwnerID:         "user-fs",
		OwnerRef:        "user://user-fs",
		Kind:            "reportBuilder.savedReportPayload",
		Lifecycle:       "draft",
		Version:         1,
		ReportID:        "fs_report",
		Title:           "FS Report",
		DocumentVersion: 2,
		Document:        []byte(`{"kind":"reportDocument","id":"fs_report"}`),
		CreatedAt:       now,
	}))
	mustNoErr(t, fsAudit.Record(context.Background(), &reportingsvc.AuditEvent{
		EventType:   "report.saved",
		ArtifactRef: "reportBuilder.savedReportPayload://fs_report",
		Version:     1,
		ArtifactID:  "shared-fs-1",
		ActorID:     "user-fs",
		ActorRef:    "user://user-fs",
		OccurredAt:  now,
		Metadata:    map[string]interface{}{"source": "fs"},
	}))

	dao, err := data.NewDatlyInMemory(context.Background())
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(context.Background(), dao, "agently", stateStore, fsClient)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(context.Background(), "user-fs")
	jobs, err := client.ListJobs(ownerCtx)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].JobID != "job-fs-1" {
		t.Fatalf("ListJobs() = %+v, want migrated job", jobs)
	}

	artifacts, err := client.ListArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListArtifacts() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].ArtifactID != "artifact-fs-1" {
		t.Fatalf("ListArtifacts() = %+v, want migrated artifact", artifacts)
	}

	shared, err := client.ListSharedArtifacts(ownerCtx)
	if err != nil {
		t.Fatalf("ListSharedArtifacts() error = %v", err)
	}
	if len(shared) != 1 || shared[0].ArtifactID != "shared-fs-1" {
		t.Fatalf("ListSharedArtifacts() = %+v, want migrated shared artifact", shared)
	}

	sqlStore, ok := client.(*Store)
	if !ok || sqlStore == nil {
		t.Fatalf("expected *Store, got %#v", client)
	}
	db, err := sqlStore.dbHandle()
	if err != nil {
		t.Fatalf("dbHandle() error = %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(1) FROM report_audit_event WHERE actor_id = ?`, "user-fs").Scan(&auditCount); err != nil {
		t.Fatalf("QueryRowContext(audit count) error = %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("auditCount = %d, want 1", auditCount)
	}
}

func TestStore_ImportsFilesystemStateWhenComponentsAlreadyRegistered(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	isolateReportingComponents(t, dao, "agently")

	if _, err = New(ctx, dao, "agently", nil, nil); err != nil {
		t.Fatalf("first New() error = %v", err)
	}

	stateStore := fsstate.NewStateStore(t.TempDir())
	fsClient := reportfs.New(stateStore)
	mustNoErr(t, fsClient.CreateJob(ctx, &reportjob.Record{
		JobID:       "job-fs-loaded-components",
		ArtifactRef: "report://draft/loaded-components",
		OwnerID:     "user-fs-loaded-components",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC),
	}))

	client, err := New(ctx, dao, "agently", stateStore, fsClient)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}

	ownerCtx := authsvc.InjectUser(ctx, "user-fs-loaded-components")
	jobs, err := client.ListJobs(ownerCtx)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0] == nil || jobs[0].JobID != "job-fs-loaded-components" {
		t.Fatalf("ListJobs() = %+v, want imported job from second Store initialization", jobs)
	}
}

func TestStore_InternalAccessBypassesOwnerScopeForWorkerFlows(t *testing.T) {
	ctx := context.Background()
	dao, err := data.NewDatlyInMemory(ctx)
	if err != nil {
		t.Fatalf("NewDatlyInMemory() error = %v", err)
	}
	client, err := New(ctx, dao, "agently", nil, reportmemory.New())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	store, ok := client.(*Store)
	if !ok || store == nil {
		t.Fatalf("expected *Store, got %#v", client)
	}

	ownerCtx := authsvc.InjectUser(ctx, "owner-1")
	internalCtx := WithInternalAccess(ctx)
	submittedAt := time.Date(2026, 7, 14, 21, 0, 0, 0, time.UTC)
	job := &reportjob.Record{
		JobID:       "job-internal-1",
		ArtifactRef: "report://preset/internal",
		OwnerID:     "owner-1",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: submittedAt,
	}
	if err := store.CreateJob(ownerCtx, job); err != nil {
		t.Fatalf("CreateJob(owner) error = %v", err)
	}

	jobs, err := store.ListJobs(internalCtx)
	if err != nil {
		t.Fatalf("ListJobs(internal) error = %v", err)
	}
	foundJob := false
	for _, item := range jobs {
		if item != nil && item.JobID == "job-internal-1" {
			foundJob = true
			break
		}
	}
	if !foundJob {
		t.Fatalf("ListJobs(internal) = %+v, want queued job-internal-1 present", jobs)
	}

	got, err := store.GetJob(internalCtx, "job-internal-1")
	if err != nil {
		t.Fatalf("GetJob(internal) error = %v", err)
	}
	if got == nil || got.OwnerID != "owner-1" {
		t.Fatalf("GetJob(internal) = %+v, want owner-scoped row", got)
	}

	got.Status = "succeeded"
	got.ArtifactID = "artifact-internal-1"
	completedAt := submittedAt.Add(2 * time.Second)
	got.CompletedAt = &completedAt
	if err := store.UpdateJob(internalCtx, got); err != nil {
		t.Fatalf("UpdateJob(internal) error = %v", err)
	}

	updated, err := store.GetJob(ownerCtx, "job-internal-1")
	if err != nil {
		t.Fatalf("GetJob(owner after internal update) error = %v", err)
	}
	if updated == nil || updated.Status != "succeeded" || updated.ArtifactID != "artifact-internal-1" {
		t.Fatalf("GetJob(owner after internal update) = %+v, want succeeded artifact row", updated)
	}

	artifact := &reportartifact.Record{
		ArtifactID:  "artifact-internal-1",
		JobID:       "job-internal-1",
		ArtifactRef: "report://preset/internal",
		OwnerID:     "owner-1",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF-internal"),
		CreatedAt:   submittedAt.Add(3 * time.Second),
	}
	if err := store.PutArtifact(internalCtx, artifact); err != nil {
		t.Fatalf("PutArtifact(internal) error = %v", err)
	}

	gotArtifact, err := store.GetArtifact(ownerCtx, "artifact-internal-1")
	if err != nil {
		t.Fatalf("GetArtifact(owner) error = %v", err)
	}
	if gotArtifact == nil || string(gotArtifact.Data) != "%PDF-internal" {
		t.Fatalf("GetArtifact(owner) = %+v, want inserted artifact", gotArtifact)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertExactT2JobColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(report_export_job)`)
	if err != nil {
		t.Fatalf("inspect report_export_job columns: %v", err)
	}
	defer rows.Close()
	t2Columns := map[string]bool{}
	for rows.Next() {
		var (
			position     int
			name         string
			columnType   string
			notNull      int
			defaultValue interface{}
			primaryKey   int
		)
		if err := rows.Scan(&position, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan report_export_job column: %v", err)
		}
		switch name {
		case "report_run_id", "report_run_revision", "export_request_id":
			t2Columns[name] = true
		case "revision":
			t.Fatal("report_export_job unexpectedly contains a fourth revision column")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("inspect report_export_job columns: %v", err)
	}
	if len(t2Columns) != 3 {
		t.Fatalf("T2 report_export_job columns = %#v, want exactly three", t2Columns)
	}
}

func isolateReportingComponents(t *testing.T, dao *datly.Service, connectorRef string) {
	t.Helper()
	key := fmt.Sprintf("%d:%s", reflect.ValueOf(dao).Pointer(), normalizeConnectorRef(connectorRef))
	reportingComponentsBy.Delete(key)
	t.Cleanup(func() {
		reportingComponentsBy.Delete(key)
	})
}
