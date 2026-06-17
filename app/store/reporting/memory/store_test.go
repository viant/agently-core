package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
)

func TestStoreCRUD(t *testing.T) {
	store := New()
	now := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	job := &reportjob.Record{
		JobID:       "job-1",
		ArtifactRef: "report://draft/demo",
		OwnerID:     "user-1",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		ReportPrint: []byte(`{"kind":"reportPrint"}`),
		SubmittedAt: now,
	}

	require.NoError(t, store.CreateJob(context.Background(), job))
	gotJob, err := store.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "queued", gotJob.Status)
	require.NotSame(t, job, gotJob)

	startedAt := now.Add(time.Minute)
	gotJob.Status = "running"
	gotJob.StartedAt = &startedAt
	require.NoError(t, store.UpdateJob(context.Background(), gotJob))

	updated, err := store.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "running", updated.Status)
	require.NotNil(t, updated.StartedAt)

	artifact := &reportartifact.Record{
		ArtifactID:  "artifact-1",
		JobID:       "job-1",
		ArtifactRef: "report://draft/demo",
		OwnerID:     "user-1",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF"),
		CreatedAt:   now,
	}
	require.NoError(t, store.PutArtifact(context.Background(), artifact))

	gotArtifact, err := store.GetArtifact(context.Background(), "artifact-1")
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF"), gotArtifact.Data)
	require.NotSame(t, artifact, gotArtifact)
}
