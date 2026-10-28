package reporting

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	identity "github.com/viant/agently-core/protocol/resource"
	authsvc "github.com/viant/agently-core/service/auth"
)

const maxExportExecutionContexts = 256

type exportExecutionContexts struct {
	mu      sync.Mutex
	entries map[string]exportExecutionContext
}

type exportExecutionContext struct {
	identity *authsvc.VerifiedContextSnapshot
	job      *ExportJob
	deadline time.Time
}

type retainedExportContextKey struct{}
type exportWorkerContextKey struct{}

func (s *Service) retainExportExecutionContext(ctx context.Context, job *ExportJob) error {
	if job.ResourcePin == nil {
		return nil
	}
	snapshot, err := authsvc.CaptureVerifiedContext(ctx)
	if err != nil {
		return err
	}
	if snapshot == nil {
		return nil
	}
	deadline := identity.MinimumMetadataExpiry(snapshot.ValidUntil(), job.ResourcePin.ValidUntil)
	c := &s.exportContexts
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]exportExecutionContext{}
	}
	for id, entry := range c.entries {
		if !entry.deadline.After(s.now()) {
			delete(c.entries, id)
		}
	}
	if len(c.entries) >= maxExportExecutionContexts {
		return fmt.Errorf("reporting export: pending verified execution capacity reached")
	}
	c.entries[job.JobID] = exportExecutionContext{identity: snapshot, job: cloneJob(job), deadline: deadline}
	return nil
}

func (s *Service) releaseExportExecutionContext(id string) {
	s.exportContexts.mu.Lock()
	delete(s.exportContexts.entries, id)
	s.exportContexts.mu.Unlock()
}

func (s *Service) restoreExportExecutionContext(ctx context.Context, job *ExportJob) (context.Context, context.CancelFunc, error) {
	s.exportContexts.mu.Lock()
	entry, found := s.exportContexts.entries[job.JobID]
	s.exportContexts.mu.Unlock()
	if !found {
		return ctx, func() {}, nil
	}
	if !entry.deadline.After(s.now()) || entry.job.OwnerID != job.OwnerID || entry.job.AuthContextRef != job.AuthContextRef || !sameResourcePin(entry.job.ResourcePin, job.ResourcePin) {
		return ctx, func() {}, identity.ErrResourceDenied
	}
	bounded, cancel := context.WithDeadline(ctx, entry.deadline)
	restored, err := entry.identity.Restore(bounded)
	if err != nil {
		cancel()
		return ctx, func() {}, errors.Join(identity.ErrResourceDenied, err)
	}
	return context.WithValue(restored, retainedExportContextKey{}, job.JobID), cancel, nil
}

// Only the internal worker may finalize an unclaimable job. This is a queue
// lifecycle transition after a failed check; it grants no resource access.
func (s *Service) failUnclaimableWorkerExport(ctx context.Context, id string, cause error) (*ExportJob, error) {
	if ctx.Value(exportWorkerContextKey{}) != true || !errors.Is(cause, identity.ErrResourceDenied) {
		return nil, cause
	}
	job, err := s.claimExportWithoutResourceRead(ctx, id)
	if err != nil {
		return nil, err
	}
	failed, err := s.failRunExport(ctx, job.JobID, cause)
	s.releaseExportExecutionContext(id)
	return failed, err
}

func (s *Service) claimExportWithoutResourceRead(ctx context.Context, id string) (*ExportJob, error) {
	if store, ok := s.store.(RunExportStore); ok {
		return store.ClaimJob(ctx, id, s.now().UTC())
	}
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNotFound
	}
	if job.Status != JobStatusQueued {
		return nil, ErrJobNotQueued
	}
	now := s.now().UTC()
	job.Status, job.StartedAt = JobStatusRunning, &now
	if err := s.store.UpdateJob(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}
