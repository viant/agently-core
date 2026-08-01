package fs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
)

func TestStore_ReconcileRunningJobRejectsMismatchedArtifact(t *testing.T) {
	store := New(fsstate.NewStateStore(t.TempDir())).(*Store)
	now := time.Date(2026, 7, 29, 20, 0, 0, 0, time.UTC)
	job := &reportjob.Record{
		JobID:       "job-reconcile-mismatch",
		ArtifactRef: "report://draft/exact",
		OwnerID:     "owner-1",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}
	require.NoError(t, store.CreateJob(context.Background(), job))
	_, err := store.ClaimJob(context.Background(), job.JobID, now)
	require.NoError(t, err)

	artifactPath, err := store.recordPath(context.Background(), "artifacts", "artifact-mismatch")
	require.NoError(t, err)
	require.NoError(t, writeJSONCreateOnly(artifactPath, &reportartifact.Record{
		ArtifactID:  "artifact-mismatch",
		JobID:       job.JobID,
		ArtifactRef: "report://draft/other",
		OwnerID:     "owner-2",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF"),
		CreatedAt:   now,
	}, "duplicate artifact: %w", reportstore.ErrAlreadyExists))

	_, err = store.ReconcileRunningJobs(context.Background(), now.Add(time.Minute), now.Add(2*time.Minute), "stale")
	require.ErrorIs(t, err, reportstore.ErrConflict)
	persisted, err := store.GetJob(context.Background(), job.JobID)
	require.NoError(t, err)
	require.Equal(t, "running", persisted.Status)
	require.Empty(t, persisted.ArtifactID)
}
