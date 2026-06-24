package fs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	fsstate "github.com/viant/agently-core/workspace/store/fs"
)

func TestStore_PersistsAcrossInstances(t *testing.T) {
	root := t.TempDir()
	stateStore := fsstate.NewStateStore(root)

	first := New(stateStore)
	now := time.Date(2026, 6, 13, 15, 0, 0, 0, time.UTC)
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
	require.NoError(t, first.CreateJob(context.Background(), job))
	require.NoError(t, first.PutArtifact(context.Background(), &reportartifact.Record{
		ArtifactID:  "artifact-1",
		JobID:       "job-1",
		ArtifactRef: "report://draft/demo",
		OwnerID:     "user-1",
		Format:      "pdf",
		ContentType: "application/pdf",
		Data:        []byte("%PDF"),
		CreatedAt:   now,
	}))
	require.NoError(t, first.CreateSharedArtifact(context.Background(), &reportshareartifact.Record{
		ArtifactID:       "shared-1",
		ArtifactRef:      "reportBuilder.savedView://saved_view_capacity_q3",
		OwnerID:          "user-1",
		Kind:             "reportBuilder.savedView",
		Lifecycle:        "published",
		Version:          8,
		ReportID:         "capacityQ3",
		Title:            "Capacity Q3 Saved View",
		SourceArtifactID: "saved_view_capacity_q3",
		DocumentVersion:  8,
		Document:         []byte(`{"kind":"reportDocument","id":"capacityQ3"}`),
		CreatedAt:        now,
	}))

	second := New(stateStore)
	gotJob, err := second.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "queued", gotJob.Status)
	require.Equal(t, []byte(`{"kind":"reportPrint"}`), gotJob.ReportPrint)

	gotArtifact, err := second.GetArtifact(context.Background(), "artifact-1")
	require.NoError(t, err)
	require.Equal(t, "application/pdf", gotArtifact.ContentType)
	require.Equal(t, []byte("%PDF"), gotArtifact.Data)

	artifacts, err := second.ListArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, artifacts, 1)
	require.Equal(t, "artifact-1", artifacts[0].ArtifactID)

	gotSharedArtifact, err := second.GetSharedArtifact(context.Background(), "shared-1")
	require.NoError(t, err)
	require.Equal(t, "reportBuilder.savedView", gotSharedArtifact.Kind)
	require.Equal(t, []byte(`{"kind":"reportDocument","id":"capacityQ3"}`), gotSharedArtifact.Document)

	sharedArtifacts, err := second.ListSharedArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, sharedArtifacts, 1)
	require.Equal(t, "shared-1", sharedArtifacts[0].ArtifactID)
}

func TestStore_RejectsInvalidIDs(t *testing.T) {
	root := t.TempDir()
	store := New(fsstate.NewStateStore(root))

	_, err := store.GetJob(context.Background(), "../bad")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid id")
}

func TestStore_RejectsDuplicateRecords(t *testing.T) {
	root := t.TempDir()
	store := New(fsstate.NewStateStore(root))
	now := time.Date(2026, 6, 13, 15, 30, 0, 0, time.UTC)

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
	require.ErrorContains(t, err, "reporting fs store: job job-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)
	gotJob, err := store.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "queued", gotJob.Status)

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
	require.ErrorContains(t, err, "reporting fs store: artifact artifact-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)
	gotArtifact, err := store.GetArtifact(context.Background(), "artifact-1")
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF"), gotArtifact.Data)

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
	require.ErrorContains(t, err, "reporting fs store: shared artifact shared-1 already exists")
	require.ErrorIs(t, err, reportstore.ErrAlreadyExists)
	gotSharedArtifact, err := store.GetSharedArtifact(context.Background(), "shared-1")
	require.NoError(t, err)
	require.Equal(t, "reportBuilder.savedView", gotSharedArtifact.Kind)
	sharedArtifacts, err := store.ListSharedArtifacts(context.Background())
	require.NoError(t, err)
	require.Len(t, sharedArtifacts, 1)
	require.Equal(t, "shared-1", sharedArtifacts[0].ArtifactID)
}
