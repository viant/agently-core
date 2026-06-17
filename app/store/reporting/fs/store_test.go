package fs

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
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

	second := New(stateStore)
	gotJob, err := second.GetJob(context.Background(), "job-1")
	require.NoError(t, err)
	require.Equal(t, "queued", gotJob.Status)
	require.Equal(t, []byte(`{"kind":"reportPrint"}`), gotJob.ReportPrint)

	gotArtifact, err := second.GetArtifact(context.Background(), "artifact-1")
	require.NoError(t, err)
	require.Equal(t, "application/pdf", gotArtifact.ContentType)
	require.Equal(t, []byte("%PDF"), gotArtifact.Data)
}

func TestStore_RejectsInvalidIDs(t *testing.T) {
	root := t.TempDir()
	store := New(fsstate.NewStateStore(root))

	_, err := store.GetJob(context.Background(), "../bad")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid id")
}
