package reporting

import (
	"context"
	"fmt"
	"sync"
)

// MemoryStore is the simplest in-process reporting store. It is intended for
// scaffolding, tests, and bootstrap paths before durable Datly persistence is
// wired in.
type MemoryStore struct {
	mu              sync.RWMutex
	jobs            map[string]*ExportJob
	artifacts       map[string]*Artifact
	sharedArtifacts map[string]*SharedArtifact
}

// NewMemoryStore constructs an empty in-memory reporting store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:            map[string]*ExportJob{},
		artifacts:       map[string]*Artifact{},
		sharedArtifacts: map[string]*SharedArtifact{},
	}
}

// CreateJob persists a queued export job.
func (s *MemoryStore) CreateJob(_ context.Context, job *ExportJob) error {
	if s == nil || job == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.JobID]; exists {
		return fmt.Errorf("reporting memory store: job %s already exists: %w", job.JobID, ErrAlreadyExists)
	}
	s.jobs[job.JobID] = cloneJob(job)
	return nil
}

// GetJob returns a cloned export job.
func (s *MemoryStore) GetJob(_ context.Context, jobID string) (*ExportJob, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneJob(job), nil
}

// ListJobs returns cloned export jobs in unspecified order.
func (s *MemoryStore) ListJobs(_ context.Context) ([]*ExportJob, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*ExportJob, 0, len(s.jobs))
	for _, job := range s.jobs {
		result = append(result, cloneJob(job))
	}
	return result, nil
}

// UpdateJob replaces a persisted export job.
func (s *MemoryStore) UpdateJob(_ context.Context, job *ExportJob) error {
	if s == nil || job == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.JobID]; !ok {
		return ErrNotFound
	}
	s.jobs[job.JobID] = cloneJob(job)
	return nil
}

// PutArtifact stores a completed export artifact.
func (s *MemoryStore) PutArtifact(_ context.Context, artifact *Artifact) error {
	if s == nil || artifact == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.artifacts[artifact.ArtifactID]; exists {
		return fmt.Errorf("reporting memory store: artifact %s already exists: %w", artifact.ArtifactID, ErrAlreadyExists)
	}
	s.artifacts[artifact.ArtifactID] = cloneArtifact(artifact)
	return nil
}

// GetArtifact returns a cloned export artifact.
func (s *MemoryStore) GetArtifact(_ context.Context, artifactID string) (*Artifact, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneArtifact(artifact), nil
}

// ListArtifacts returns cloned export artifacts in unspecified order.
func (s *MemoryStore) ListArtifacts(_ context.Context) ([]*Artifact, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Artifact, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		result = append(result, cloneArtifact(artifact))
	}
	return result, nil
}

// CreateSharedArtifact persists a shared reporting artifact.
func (s *MemoryStore) CreateSharedArtifact(_ context.Context, artifact *SharedArtifact) error {
	if s == nil || artifact == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sharedArtifacts[artifact.ArtifactID]; exists {
		return fmt.Errorf("reporting memory store: shared artifact %s already exists: %w", artifact.ArtifactID, ErrAlreadyExists)
	}
	s.sharedArtifacts[artifact.ArtifactID] = cloneMemorySharedArtifact(artifact)
	return nil
}

// GetSharedArtifact returns a cloned shared reporting artifact.
func (s *MemoryStore) GetSharedArtifact(_ context.Context, artifactID string) (*SharedArtifact, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.sharedArtifacts[artifactID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneMemorySharedArtifact(artifact), nil
}

// ListSharedArtifacts returns cloned shared reporting artifacts in unspecified order.
func (s *MemoryStore) ListSharedArtifacts(_ context.Context) ([]*SharedArtifact, error) {
	if s == nil {
		return nil, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*SharedArtifact, 0, len(s.sharedArtifacts))
	for _, artifact := range s.sharedArtifacts {
		result = append(result, cloneMemorySharedArtifact(artifact))
	}
	return result, nil
}

// UpdateSharedArtifact replaces a persisted shared reporting artifact.
func (s *MemoryStore) UpdateSharedArtifact(_ context.Context, artifact *SharedArtifact) error {
	if s == nil || artifact == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sharedArtifacts[artifact.ArtifactID]; !ok {
		return ErrNotFound
	}
	s.sharedArtifacts[artifact.ArtifactID] = cloneMemorySharedArtifact(artifact)
	return nil
}

func cloneMemorySharedArtifact(input *SharedArtifact) *SharedArtifact {
	if input == nil {
		return nil
	}
	out := *input
	if len(input.Document) > 0 {
		out.Document = cloneJSON(input.Document)
	}
	if len(input.ReportSpec) > 0 {
		out.ReportSpec = cloneJSON(input.ReportSpec)
	}
	if len(input.ReportFill) > 0 {
		out.ReportFill = cloneJSON(input.ReportFill)
	}
	if len(input.ReportPrint) > 0 {
		out.ReportPrint = cloneJSON(input.ReportPrint)
	}
	if len(input.SavedViewOverlay) > 0 {
		out.SavedViewOverlay = cloneJSON(input.SavedViewOverlay)
	}
	if len(input.Metadata) > 0 {
		out.Metadata = cloneJSON(input.Metadata)
	}
	if input.UpdatedAt != nil {
		updatedAt := *input.UpdatedAt
		out.UpdatedAt = &updatedAt
	}
	return &out
}
