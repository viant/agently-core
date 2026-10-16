package sql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/agently-core/internal/auth"
	artifactmodel "github.com/viant/agently-core/model/reportartifact"
	jobmodel "github.com/viant/agently-core/model/reportjob"
)

func TestNativeExportCompletionLateFailureRollsBackArtifact(t *testing.T) {
	ctx, store, db, _ := reportingRuntime(t)
	owner := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: "u1"})
	worker := WithInternalAccess(ctx)
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	job := &jobmodel.Record{JobID: "completion-rollback", ArtifactRef: "report://local/rollback", OwnerID: "u1", Format: "pdf", Scope: "draft", Status: "queued", SubmittedAt: at}
	require.NoError(t, store.CreateJob(owner, job))
	_, err := store.ClaimJob(worker, job.JobID, at.Add(time.Second))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TRIGGER reject_export_completion BEFORE UPDATE OF status ON report_export_job WHEN NEW.job_id='completion-rollback' AND NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'owned late completion failure'); END`)
	require.NoError(t, err)
	artifact := &artifactmodel.Record{ArtifactID: "completion-rollback-artifact", JobID: job.JobID, ContentType: "application/pdf", Data: []byte("%PDF must not persist"), CreatedAt: at.Add(2 * time.Second)}
	_, err = store.CompleteJobWithArtifact(worker, job.JobID, artifact, nil, at.Add(3*time.Second), 0)
	require.Error(t, err)
	require.ErrorContains(t, err, "owned late completion failure")
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM report_export_artifact WHERE artifact_id=?", artifact.ArtifactID).Scan(&count))
	require.Zero(t, count)
	current, err := store.GetJob(owner, job.JobID)
	require.NoError(t, err)
	require.Equal(t, "running", current.Status)
	require.Empty(t, current.ArtifactID)
}
