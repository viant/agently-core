package sql

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	authctx "github.com/viant/agently-core/internal/auth"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
	"github.com/viant/datly/standalone"
)

func reportingRuntime(t *testing.T) (context.Context, *Store, *sql.DB, *standalone.Server) {
	t.Helper()
	t.Setenv("AGENTLY_DB_DRIVER", "")
	t.Setenv("AGENTLY_DB_DSN", "")
	t.Setenv("AGENTLY_DB_PATH", "")
	t.Setenv("AGENTLY_DB_SECRETS", "")
	_, file, _, _ := runtime.Caller(0)
	sourceRoot := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	workspaceRoot := t.TempDir()
	ctx := context.Background()
	server, err := native.New(ctx, native.Options{SourceRoot: sourceRoot, WorkspaceRoot: workspaceRoot})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	db, err := sql.Open("sqlite3", filepath.Join(workspaceRoot, "db", "agently-core.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	client, err := New(ctx, server, nil, nil)
	require.NoError(t, err)
	return ctx, client.(*Store), db, server
}

func TestNativeReportingStoreLifecycle(t *testing.T) {
	ctx, store, db, _ := reportingRuntime(t)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	other := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u2"})
	_, err := db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES('c1')")
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	run := &reportrun.Record{ReportRunID: "run-1", OwnerID: "u1", Materializer: "test", Origin: "manual", Status: "running", StartedAt: at, Revision: 1, UIRunRequestID: "request-1", ReportSpec: []byte(`{}`), CreatedAt: at, UpdatedAt: at}
	require.NoError(t, store.CreateReportRun(owner, run))
	gotRun, err := store.GetReportRunByRequestID(owner, run.UIRunRequestID)
	require.NoError(t, err)
	require.Equal(t, run.ReportRunID, gotRun.ReportRunID)
	_, err = store.GetReportRun(other, run.ReportRunID)
	require.ErrorIs(t, err, reportstore.ErrNotFound)
	run.Revision = 2
	run.Status = "completed"
	completed := at.Add(time.Hour)
	run.CompletedAt = &completed
	run.UpdatedAt = completed
	require.NoError(t, store.UpdateReportRunCAS(owner, run, 1))
	require.ErrorIs(t, store.UpdateReportRunCAS(owner, run, 1), reportstore.ErrCASMismatch)
	pointer := &reportcontext.Record{OwnerID: "u1", ConversationID: "c1", ActiveReportRunID: "run-1", Revision: 1, ActivationSource: "manual", ActorID: "u1", UpdatedAt: completed}
	require.NoError(t, store.PutConversationReportContextCAS(owner, pointer, 0))
	gotPointer, err := store.GetConversationReportContext(owner, "c1")
	require.NoError(t, err)
	require.Equal(t, pointer.ActiveReportRunID, gotPointer.ActiveReportRunID)
	job := &reportjob.Record{JobID: "job-1", ArtifactRef: "report://one", OwnerID: "u1", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at}
	require.NoError(t, store.CreateJob(owner, job))
	_, err = store.GetJob(other, job.JobID)
	require.True(t, errors.Is(err, errNotFound))
	claimed, err := store.ClaimJob(owner, job.JobID, completed)
	require.NoError(t, err)
	require.Equal(t, "running", claimed.Status)
	_, err = store.ClaimJob(owner, job.JobID, completed)
	require.ErrorIs(t, err, reportstore.ErrInvalidTransition)
	failed, err := store.FailJob(owner, job.JobID, "worker failed", []byte(`{"reason":"test"}`), completed.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, "failed", failed.Status)
	artifact := &reportartifact.Record{ArtifactID: "artifact-1", JobID: job.JobID, ArtifactRef: job.ArtifactRef, OwnerID: "u1", Format: "pdf", ContentType: "application/pdf", Data: []byte("%PDF"), CreatedAt: completed}
	require.NoError(t, store.PutArtifact(owner, artifact))
	gotArtifact, err := store.GetArtifact(owner, artifact.ArtifactID)
	require.NoError(t, err)
	require.Equal(t, artifact.Data, gotArtifact.Data)
	shared := &reportshareartifact.Record{ArtifactID: "shared-1", ArtifactRef: "report://saved", OwnerID: "u1", Kind: "saved", Lifecycle: "draft", Version: 1, ReportID: "report-1", Title: "Saved", DocumentVersion: 1, Document: []byte(`{}`), CreatedAt: at}
	require.NoError(t, store.CreateSharedArtifact(owner, shared))
	items, err := store.ListSharedArtifacts(owner)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, store.DeleteSharedArtifact(owner, shared.ArtifactID))
	require.NoError(t, NewAuditSink(store).Record(owner, &reportingsvc.AuditEvent{EventType: "report.saved", ArtifactRef: "report://saved", ActorID: "u1", OccurredAt: completed}))
	var audits int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM report_audit_event WHERE actor_id='u1'").Scan(&audits))
	require.Equal(t, 1, audits)
}

func TestNativeReportingStoreRunExport(t *testing.T) {
	ctx, store, db, _ := reportingRuntime(t)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	_, err := db.ExecContext(ctx, "INSERT INTO conversation(id) VALUES('c1')")
	require.NoError(t, err)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	completed := at.Add(time.Hour)
	run := &reportrun.Record{ReportRunID: "run-export", OwnerID: "u1", ConversationID: "c1", Materializer: "test", Origin: "manual", Status: "completed", StartedAt: at, CompletedAt: &completed, Revision: 2, UIRunRequestID: "run-request", ReportSpec: []byte(`{}`), ReportFill: []byte(`{}`), ReportPrint: []byte(`{}`), CreatedAt: at, UpdatedAt: completed}
	require.NoError(t, store.CreateReportRun(owner, run))
	candidate := &reportjob.Record{JobID: "job-export", ArtifactRef: "report-run://run-export", OwnerID: "u1", ConversationID: "c1", ReportRunID: run.ReportRunID, ExportRequestID: "export-request", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: completed}
	job, replay, err := store.SubmitJobFromRun(owner, candidate)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, run.Revision, job.ReportRunRevision)
	_, replay, err = store.SubmitJobFromRun(owner, candidate)
	require.NoError(t, err)
	require.True(t, replay)
	worker := WithInternalAccess(ctx)
	_, err = store.ClaimJob(worker, job.JobID, completed.Add(time.Minute))
	require.NoError(t, err)
	artifact := &reportartifact.Record{ArtifactID: "artifact-export", JobID: job.JobID, ContentType: "application/pdf", Data: []byte("%PDF"), CreatedAt: completed.Add(2 * time.Minute)}
	done, err := store.CompleteJobWithArtifact(worker, job.JobID, artifact, nil, completed.Add(3*time.Minute), 0)
	require.NoError(t, err)
	require.Equal(t, "succeeded", done.Status)
	_, err = store.CompleteJobWithArtifact(worker, job.JobID, artifact, nil, completed.Add(3*time.Minute), 0)
	require.NoError(t, err)
}

func TestNativeReportingStoreImportsFilesystemState(t *testing.T) {
	ctx, _, db, handle := reportingRuntime(t)
	stateStore := fsstate.NewStateStore(t.TempDir())
	fsClient := reportfs.New(stateStore)
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	require.NoError(t, fsClient.CreateJob(ctx, &reportjob.Record{JobID: "fs-job", ArtifactRef: "report://fs", OwnerID: "u1", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at}))
	require.NoError(t, fsClient.PutArtifact(ctx, &reportartifact.Record{ArtifactID: "fs-artifact", JobID: "fs-job", ArtifactRef: "report://fs", OwnerID: "u1", Format: "pdf", ContentType: "application/pdf", Data: []byte("%PDF"), CreatedAt: at}))
	require.NoError(t, fsClient.CreateSharedArtifact(ctx, &reportshareartifact.Record{ArtifactID: "fs-shared", ArtifactRef: "report://saved", OwnerID: "u1", Kind: "saved", Lifecycle: "draft", Version: 1, ReportID: "report-1", Title: "Saved", DocumentVersion: 1, Document: []byte(`{}`), CreatedAt: at}))
	require.NoError(t, reportfs.NewAuditSink(stateStore).Record(ctx, &reportingsvc.AuditEvent{EventType: "report.saved", ArtifactRef: "report://saved", ArtifactID: "fs-shared", ActorID: "u1", OccurredAt: at}))
	for i := 0; i < 2; i++ {
		client, err := New(ctx, handle, stateStore, fsClient)
		require.NoError(t, err)
		owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
		job, err := client.GetJob(owner, "fs-job")
		require.NoError(t, err)
		require.Equal(t, "report://fs", job.ArtifactRef)
		artifact, err := client.GetArtifact(owner, "fs-artifact")
		require.NoError(t, err)
		require.Equal(t, []byte("%PDF"), artifact.Data)
		shared, err := client.GetSharedArtifact(owner, "fs-shared")
		require.NoError(t, err)
		require.Equal(t, "Saved", shared.Title)
	}
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM report_audit_event WHERE actor_id='u1'").Scan(&count))
	require.Equal(t, 1, count)
}
