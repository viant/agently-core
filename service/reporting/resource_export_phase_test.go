package reporting

import (
	"context"
	"errors"
	"testing"

	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
	"github.com/viant/agently-core/service/reporting/catalog"
)

// Admission scopes are read phases, not persistence transactions. Finishing a
// phase must precede its first queue/artifact write; later store checks stay fresh.
func TestExportReadPhaseFinishesBeforePersistence(t *testing.T) {
	t.Run("submit denied phase writes no job", func(t *testing.T) {
		service, store, _, pin := newPinnedExportService(t, nil)
		service.reportCatalog = &catalog.ReportCatalogService{BeginMetadataRead: func(ctx context.Context) (context.Context, func() error, error) {
			return ctx, func() error { return identity.ErrResourceDenied }, nil
		}}
		ctx := authsvc.InjectUser(context.Background(), "owner")
		_, err := service.SubmitExport(ctx, &SubmitExportRequest{Format: ExportFormatPDF, execution: trustedExecution(pin)})
		if !errors.Is(err, identity.ErrResourceDenied) {
			t.Fatalf("expected phase denial: %v", err)
		}
		jobs, _ := store.ListJobs(ctx)
		if len(jobs) != 0 {
			t.Fatalf("phase denial persisted %d jobs", len(jobs))
		}
	})
	t.Run("renderer phase denial fails job without artifact", func(t *testing.T) {
		exporter := &pinAwareExporter{}
		service, store, _, pin := newPinnedExportService(t, exporter)
		job := submitPinnedExport(t, service, pin)
		finishes := 0
		service.reportCatalog = &catalog.ReportCatalogService{BeginMetadataRead: func(ctx context.Context) (context.Context, func() error, error) {
			return ctx, func() error { finishes++; return identity.ErrResourceDenied }, nil
		}}
		ctx := authsvc.InjectUser(context.Background(), "owner")
		_, err := service.RunExport(ctx, job.JobID)
		if !errors.Is(err, identity.ErrResourceDenied) {
			t.Fatalf("expected phase denial: %v", err)
		}
		artifacts, _ := store.ListArtifacts(ctx)
		persisted, _ := store.GetJob(ctx, job.JobID)
		if finishes != 1 || exporter.calls != 1 || len(artifacts) != 0 || persisted.Status != JobStatusFailed || persisted.ArtifactID != "" {
			t.Fatalf("finish=%d render=%d artifacts=%d persisted=%+v", finishes, exporter.calls, len(artifacts), persisted)
		}
	})
}

type exportMutationStore struct {
	Store
	mutate func()
}

func (s *exportMutationStore) CreateJob(ctx context.Context, job *ExportJob) error {
	s.mutate()
	return s.Store.CreateJob(ctx, job)
}
func (s *exportMutationStore) PutArtifact(ctx context.Context, artifact *Artifact) error {
	s.mutate()
	return s.Store.PutArtifact(ctx, artifact)
}

func TestExportStoreMutationRetainsFreshPostWriteDenial(t *testing.T) {
	service, memory, state, pin := newPinnedExportService(t, nil)
	phaseFinished := false
	service.reportCatalog = &catalog.ReportCatalogService{BeginMetadataRead: func(ctx context.Context) (context.Context, func() error, error) {
		return ctx, func() error { phaseFinished = true; return nil }, nil
	}}
	service.store = &exportMutationStore{Store: memory, mutate: func() {
		if !phaseFinished {
			t.Error("store entered before metadata read phase finished")
		}
		state.mu.Lock()
		state.binding = "changed-at-store"
		state.mu.Unlock()
	}}
	ctx := authsvc.InjectUser(context.Background(), "owner")
	_, err := service.SubmitExport(ctx, &SubmitExportRequest{Format: ExportFormatPDF, execution: trustedExecution(pin)})
	if !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("store mutation bypassed fresh check: %v", err)
	}
	// Store is not a distributed resource transaction. Preserve the existing
	// queued record and denied response; never pretend the write was rolled back.
	jobs, _ := memory.ListJobs(ctx)
	if len(jobs) != 1 || jobs[0].Status != JobStatusQueued {
		t.Fatalf("unexpected store behavior: %+v", jobs)
	}
	artifacts, _ := memory.ListArtifacts(ctx)
	if len(artifacts) != 0 {
		t.Fatalf("submit published artifacts: %d", len(artifacts))
	}
}

func TestExportCompletionStoreMutationRetainsFreshDenial(t *testing.T) {
	exporter := &pinAwareExporter{}
	service, memory, state, pin := newPinnedExportService(t, exporter)
	job := submitPinnedExport(t, service, pin)
	phaseFinished := false
	service.reportCatalog = &catalog.ReportCatalogService{BeginMetadataRead: func(ctx context.Context) (context.Context, func() error, error) {
		return ctx, func() error { phaseFinished = true; return nil }, nil
	}}
	service.store = &exportMutationStore{Store: memory, mutate: func() {
		if !phaseFinished {
			t.Error("artifact store entered before metadata phase finished")
		}
		state.mu.Lock()
		state.binding = "changed-at-artifact-store"
		state.mu.Unlock()
	}}
	ctx := authsvc.InjectUser(context.Background(), "owner")
	_, err := service.RunExport(ctx, job.JobID)
	if !errors.Is(err, identity.ErrResourceDenied) {
		t.Fatalf("store mutation bypassed fresh check: %v", err)
	}
	// A generic Store has no resource rollback hook: this is the existing
	// post-write denial behavior, not a deferred phase error or failed-job rewrite.
	persisted, _ := memory.GetJob(ctx, job.JobID)
	artifacts, _ := memory.ListArtifacts(ctx)
	if persisted.Status != JobStatusSucceeded || persisted.ArtifactID == "" || len(artifacts) != 1 {
		t.Fatalf("changed persistence contract: job=%+v artifacts=%d", persisted, len(artifacts))
	}
	if _, err := service.GetArtifact(ctx, persisted.ArtifactID); !errors.Is(err, identity.ErrResourceDenied) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("changed authority downloaded bytes: %v", err)
	}
}
