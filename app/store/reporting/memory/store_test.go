package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	authsvc "github.com/viant/agently-core/service/auth"
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

func TestStore_AdoptionIsCompositeAndCompletedSnapshotsAreImmutable(t *testing.T) {
	store := New().(reportstore.RunClient)
	ctx := authsvc.InjectUser(context.Background(), "owner-1")
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)

	prior := completedMemoryRun("prior-run", "owner-1", "conv-stale", 2, now)
	require.NoError(t, store.CreateReportRun(ctx, prior))
	require.NoError(t, store.PutConversationReportContextCAS(ctx, &reportcontext.Record{
		OwnerID:           "owner-1",
		ConversationID:    "conv-stale",
		ActiveReportRunID: prior.ReportRunID,
		Revision:          1,
		ActivationSource:  "manual",
		ActorID:           "owner-1",
		UpdatedAt:         now,
	}, 0))
	manual := completedMemoryRun("manual-run", "owner-1", "", 2, now)
	manual.Origin = "manual"
	require.NoError(t, store.CreateReportRun(ctx, manual))
	adopted := adoptedMemoryRun(manual, "conv-stale", now.Add(time.Second))
	pointer := adoptedMemoryContext(adopted, 1, now.Add(time.Second))

	require.ErrorIs(t, store.AdoptReportRunAndContextCAS(ctx, adopted, 2, pointer, 0), reportstore.ErrCASMismatch)
	unchangedRun, err := store.GetReportRun(ctx, manual.ReportRunID)
	require.NoError(t, err)
	require.Empty(t, unchangedRun.ConversationID)
	unchangedPointer, err := store.GetConversationReportContext(ctx, "conv-stale")
	require.NoError(t, err)
	require.Equal(t, prior.ReportRunID, unchangedPointer.ActiveReportRunID)

	mutatedSnapshot := *manual
	mutatedSnapshot.ReportSpec = []byte(`{"kind":"changed"}`)
	mutatedSnapshot.Revision++
	require.ErrorIs(t, store.UpdateReportRunCAS(ctx, &mutatedSnapshot, manual.Revision), reportstore.ErrImmutable)

	concurrent := completedMemoryRun("manual-concurrent", "owner-1", "", 2, now)
	concurrent.Origin = "manual"
	require.NoError(t, store.CreateReportRun(ctx, concurrent))
	next := adoptedMemoryRun(concurrent, "conv-concurrent", now.Add(2*time.Second))
	nextPointer := adoptedMemoryContext(next, 0, now.Add(2*time.Second))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- store.AdoptReportRunAndContextCAS(ctx, next, 2, nextPointer, 0)
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
			t.Fatalf("concurrent adoption error = %v", result)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, casFailures)
	gotRun, err := store.GetReportRun(ctx, concurrent.ReportRunID)
	require.NoError(t, err)
	require.Equal(t, int64(3), gotRun.Revision)
	gotPointer, err := store.GetConversationReportContext(ctx, "conv-concurrent")
	require.NoError(t, err)
	require.Equal(t, int64(1), gotPointer.Revision)
	require.Equal(t, concurrent.ReportRunID, gotPointer.ActiveReportRunID)
}

func completedMemoryRun(id, ownerID, conversationID string, revision int64, now time.Time) *reportrun.Record {
	completedAt := now
	return &reportrun.Record{
		ReportRunID:    id,
		OwnerID:        ownerID,
		ConversationID: conversationID,
		Materializer:   reportrun.MaterializerLegacyBrowser,
		Origin:         "prompt",
		Status:         reportrun.StatusCompleted,
		Revision:       revision,
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

func adoptedMemoryRun(current *reportrun.Record, conversationID string, now time.Time) *reportrun.Record {
	next := *current
	next.ConversationID = conversationID
	next.AdoptionSource = "adopt"
	next.ActorID = current.OwnerID
	next.Revision++
	next.UpdatedAt = now
	return &next
}

func adoptedMemoryContext(run *reportrun.Record, expectedRevision int64, now time.Time) *reportcontext.Record {
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
