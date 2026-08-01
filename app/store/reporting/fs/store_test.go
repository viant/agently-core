package fs

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

func TestStore_ReportRunAndActivePointerPersistAcrossInstances(t *testing.T) {
	stateStore := fsstate.NewStateStore(t.TempDir())
	first, ok := New(stateStore).(reportstore.RunClient)
	require.True(t, ok)
	ctx := authsvc.InjectUser(context.Background(), "user-1")
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	completedAt := now.Add(time.Second)
	run := &reportrun.Record{
		ReportRunID:    "run-persisted-1",
		OwnerID:        "user-1",
		ConversationID: "conv-1",
		Materializer:   reportrun.MaterializerLegacyBrowser,
		Status:         reportrun.StatusCompleted,
		Revision:       2,
		UIRunRequestID: "ui-request-persisted-1",
		ReportSpec:     []byte(`{"kind":"reportSpec"}`),
		ReportFill:     []byte(`{"kind":"reportFill"}`),
		ReportPrint:    []byte(`{"kind":"reportPrint"}`),
		StartedAt:      now,
		CompletedAt:    &completedAt,
		CreatedAt:      now,
		UpdatedAt:      completedAt,
	}
	require.NoError(t, first.CreateReportRun(ctx, run))
	require.NoError(t, first.PutConversationReportContextCAS(ctx, &reportcontext.Record{
		OwnerID:           "user-1",
		ConversationID:    "conv-1",
		ActiveReportRunID: "run-persisted-1",
		Revision:          1,
		ActivationSource:  "prompt",
		ActorID:           "user-1",
		UpdatedAt:         completedAt,
	}, 0))

	second, ok := New(stateStore).(reportstore.RunClient)
	require.True(t, ok)
	got, err := second.GetReportRun(ctx, "run-persisted-1")
	require.NoError(t, err)
	require.Equal(t, reportrun.StatusCompleted, got.Status)
	require.Equal(t, []byte(`{"kind":"reportPrint"}`), []byte(got.ReportPrint))
	byRequest, err := second.GetReportRunByRequestID(ctx, "ui-request-persisted-1")
	require.NoError(t, err)
	require.Equal(t, "run-persisted-1", byRequest.ReportRunID)
	duplicate := *run
	duplicate.ReportRunID = "run-persisted-2"
	require.ErrorIs(t, second.CreateReportRun(ctx, &duplicate), reportstore.ErrAlreadyExists)
	active, err := second.GetConversationReportContext(ctx, "conv-1")
	require.NoError(t, err)
	require.Equal(t, "run-persisted-1", active.ActiveReportRunID)
	otherCtx := authsvc.InjectUser(context.Background(), "user-2")
	_, err = second.GetReportRun(otherCtx, "run-persisted-1")
	require.ErrorIs(t, err, reportstore.ErrNotFound)
}

func TestStore_AdoptionCASIsCompositeAcrossInstances(t *testing.T) {
	stateStore := fsstate.NewStateStore(t.TempDir())
	first := New(stateStore).(reportstore.RunClient)
	second := New(stateStore).(reportstore.RunClient)
	ctx := authsvc.InjectUser(context.Background(), "owner-adopt")
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)

	prior := completedFSRun("prior-run", "owner-adopt", "conv-stale", now)
	require.NoError(t, first.CreateReportRun(ctx, prior))
	require.NoError(t, first.PutConversationReportContextCAS(ctx, &reportcontext.Record{
		OwnerID:           "owner-adopt",
		ConversationID:    "conv-stale",
		ActiveReportRunID: prior.ReportRunID,
		Revision:          1,
		ActivationSource:  "manual",
		ActorID:           "owner-adopt",
		UpdatedAt:         now,
	}, 0))
	manual := completedFSRun("manual-run", "owner-adopt", "", now)
	manual.Origin = "manual"
	require.NoError(t, first.CreateReportRun(ctx, manual))
	staleRun := adoptedFSRun(manual, "conv-stale", now.Add(time.Second))
	stalePointer := adoptedFSContext(staleRun, 1, now.Add(time.Second))
	require.ErrorIs(t, first.AdoptReportRunAndContextCAS(ctx, staleRun, 2, stalePointer, 0), reportstore.ErrCASMismatch)
	unchangedRun, err := second.GetReportRun(ctx, manual.ReportRunID)
	require.NoError(t, err)
	require.Empty(t, unchangedRun.ConversationID)
	unchangedPointer, err := second.GetConversationReportContext(ctx, "conv-stale")
	require.NoError(t, err)
	require.Equal(t, prior.ReportRunID, unchangedPointer.ActiveReportRunID)

	mutated := *manual
	mutated.ReportPrint = []byte(`{"kind":"changed"}`)
	mutated.Revision++
	require.ErrorIs(t, first.UpdateReportRunCAS(ctx, &mutated, manual.Revision), reportstore.ErrImmutable)

	concurrent := completedFSRun("manual-concurrent", "owner-adopt", "", now)
	concurrent.Origin = "manual"
	require.NoError(t, first.CreateReportRun(ctx, concurrent))
	next := adoptedFSRun(concurrent, "conv-concurrent", now.Add(2*time.Second))
	nextPointer := adoptedFSContext(next, 0, now.Add(2*time.Second))
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []reportstore.RunClient{first, second} {
		wg.Add(1)
		go func(store reportstore.RunClient) {
			defer wg.Done()
			results <- store.AdoptReportRunAndContextCAS(ctx, next, 2, nextPointer, 0)
		}(store)
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
			t.Fatalf("concurrent filesystem adoption error = %v", result)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, casFailures)

	restarted := New(stateStore).(reportstore.RunClient)
	gotRun, err := restarted.GetReportRun(ctx, concurrent.ReportRunID)
	require.NoError(t, err)
	require.Equal(t, "conv-concurrent", gotRun.ConversationID)
	require.Equal(t, int64(3), gotRun.Revision)
	gotPointer, err := restarted.GetConversationReportContext(ctx, "conv-concurrent")
	require.NoError(t, err)
	require.Equal(t, concurrent.ReportRunID, gotPointer.ActiveReportRunID)
	require.Equal(t, int64(1), gotPointer.Revision)

	crossTarget := completedFSRun("manual-cross-target", "owner-adopt", "", now)
	crossTarget.Origin = "manual"
	require.NoError(t, first.CreateReportRun(ctx, crossTarget))
	results = make(chan error, 2)
	for _, conversationID := range []string{"conv-target-a", "conv-target-b"} {
		wg.Add(1)
		go func(conversationID string) {
			defer wg.Done()
			nextRun := adoptedFSRun(crossTarget, conversationID, now.Add(3*time.Second))
			results <- second.AdoptReportRunAndContextCAS(
				ctx,
				nextRun,
				crossTarget.Revision,
				adoptedFSContext(nextRun, 0, now.Add(3*time.Second)),
				0,
			)
		}(conversationID)
	}
	wg.Wait()
	close(results)
	successes = 0
	casFailures = 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, reportstore.ErrCASMismatch):
			casFailures++
		default:
			t.Fatalf("cross-target filesystem adoption error = %v", result)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, casFailures)
	gotRun, err = first.GetReportRun(ctx, crossTarget.ReportRunID)
	require.NoError(t, err)
	require.Contains(t, []string{"conv-target-a", "conv-target-b"}, gotRun.ConversationID)
	for _, conversationID := range []string{"conv-target-a", "conv-target-b"} {
		pointer, pointerErr := first.GetConversationReportContext(ctx, conversationID)
		if conversationID == gotRun.ConversationID {
			require.NoError(t, pointerErr)
			require.Equal(t, crossTarget.ReportRunID, pointer.ActiveReportRunID)
			continue
		}
		require.ErrorIs(t, pointerErr, reportstore.ErrNotFound)
	}
}

func completedFSRun(id, ownerID, conversationID string, now time.Time) *reportrun.Record {
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

func adoptedFSRun(current *reportrun.Record, conversationID string, now time.Time) *reportrun.Record {
	next := *current
	next.ConversationID = conversationID
	next.AdoptionSource = "adopt"
	next.ActorID = current.OwnerID
	next.Revision++
	next.UpdatedAt = now
	return &next
}

func adoptedFSContext(run *reportrun.Record, expectedRevision int64, now time.Time) *reportcontext.Record {
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
