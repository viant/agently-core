package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
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

	artifacts, err := store.ListArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "artifact-1", artifacts[0].ArtifactID)

	sharedArtifact := &reportshareartifact.Record{
		ArtifactID:       "shared-1",
		ArtifactRef:      "reportBuilder.savedView://saved_view_capacity_q3",
		OwnerID:          "user-1",
		Kind:             "reportBuilder.savedView",
		Lifecycle:        "published",
		Version:          8,
		ReportID:         "capacityQ3",
		Title:            "Capacity Q3 Saved View",
		SourceArtifactID: "saved_view_capacity_q3",
		Document:         []byte(`{"kind":"reportDocument","id":"capacityQ3"}`),
		CreatedAt:        now,
	}
	require.NoError(t, store.CreateSharedArtifact(context.Background(), sharedArtifact))
	gotSharedArtifact, err := store.GetSharedArtifact(context.Background(), "shared-1")
	require.NoError(t, err)
	require.Equal(t, []byte(`{"kind":"reportDocument","id":"capacityQ3"}`), gotSharedArtifact.Document)
	require.NotSame(t, sharedArtifact, gotSharedArtifact)
	sharedArtifacts, err := store.ListSharedArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, sharedArtifacts, 1)
	require.Equal(t, "shared-1", sharedArtifacts[0].ArtifactID)
}

func TestStoreRejectsDuplicateRecords(t *testing.T) {
	store := New()
	now := time.Date(2026, 6, 13, 10, 30, 0, 0, time.UTC)
	job := &reportjob.Record{
		JobID:       "job-1",
		ArtifactRef: "report://draft/demo",
		OwnerID:     "user-1",
		Format:      "pdf",
		Scope:       "draft",
		Status:      "queued",
		SubmittedAt: now,
	}
	require.NoError(t, store.CreateJob(context.Background(), job))
	err := store.CreateJob(context.Background(), job)
	require.Error(t, err)
	require.ErrorContains(t, err, "reporting memory store: job job-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)

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
	err = store.PutArtifact(context.Background(), artifact)
	require.Error(t, err)
	require.ErrorContains(t, err, "reporting memory store: artifact artifact-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)

	artifacts, err := store.ListArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "artifact-1", artifacts[0].ArtifactID)

	sharedArtifact := &reportshareartifact.Record{
		ArtifactID:       "shared-1",
		ArtifactRef:      "reportBuilder.savedView://saved_view_capacity_q3",
		OwnerID:          "user-1",
		Kind:             "reportBuilder.savedView",
		Lifecycle:        "published",
		Version:          8,
		ReportID:         "capacityQ3",
		Title:            "Capacity Q3 Saved View",
		SourceArtifactID: "saved_view_capacity_q3",
		CreatedAt:        now,
	}
	require.NoError(t, store.CreateSharedArtifact(context.Background(), sharedArtifact))
	err = store.CreateSharedArtifact(context.Background(), sharedArtifact)
	require.Error(t, err)
	require.ErrorContains(t, err, "reporting memory store: shared artifact shared-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)
	sharedArtifacts, err := store.ListSharedArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, sharedArtifacts, 1)
	require.Equal(t, "shared-1", sharedArtifacts[0].ArtifactID)
}
