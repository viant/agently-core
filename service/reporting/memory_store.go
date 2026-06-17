package reporting

import (
	"context"
	"sync"
)

// MemoryStore is the simplest in-process reporting store. It is intended for
// scaffolding, tests, and bootstrap paths before durable Datly persistence is
// wired in.
type MemoryStore struct {
	mu        sync.RWMutex
	jobs      map[string]*ExportJob
	artifacts map[string]*Artifact
}

// NewMemoryStore constructs an empty in-memory reporting store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:      map[string]*ExportJob{},
		artifacts: map[string]*Artifact{},
	}
}

// CreateJob persists a queued export job.
func (s *MemoryStore) CreateJob(_ context.Context, job *ExportJob) error {
	if s == nil || job == nil {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
