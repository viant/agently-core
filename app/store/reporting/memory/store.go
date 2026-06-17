package memory

import (
	"context"
	"errors"
	"sync"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
)

var errNotFound = errors.New("reporting memory store: not found")

// Store is an in-memory reporting persistence implementation.
type Store struct {
	mu        sync.RWMutex
	jobs      map[string]*reportjob.Record
	artifacts map[string]*reportartifact.Record
}

// New constructs an empty in-memory reporting store.
func New() reportstore.Client {
	return &Store{
		jobs:      map[string]*reportjob.Record{},
		artifacts: map[string]*reportartifact.Record{},
	}
}

// CreateJob persists a new job.
func (s *Store) CreateJob(_ context.Context, job *reportjob.Record) error {
	if s == nil || job == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.JobID] = cloneJob(job)
	return nil
}

// GetJob loads a stored job.
func (s *Store) GetJob(_ context.Context, jobID string) (*reportjob.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return nil, errNotFound
	}
	return cloneJob(job), nil
}

// UpdateJob replaces a stored job.
func (s *Store) UpdateJob(_ context.Context, job *reportjob.Record) error {
	if s == nil || job == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[job.JobID]; !ok {
		return errNotFound
	}
	s.jobs[job.JobID] = cloneJob(job)
	return nil
}

// PutArtifact stores a finished artifact.
func (s *Store) PutArtifact(_ context.Context, artifact *reportartifact.Record) error {
	if s == nil || artifact == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[artifact.ArtifactID] = cloneArtifact(artifact)
	return nil
}

// GetArtifact loads a stored artifact.
func (s *Store) GetArtifact(_ context.Context, artifactID string) (*reportartifact.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return nil, errNotFound
	}
	return cloneArtifact(artifact), nil
}

func cloneJob(input *reportjob.Record) *reportjob.Record {
	if input == nil {
		return nil
	}
	out := *input
	if len(input.ReportSpec) > 0 {
		out.ReportSpec = append([]byte{}, input.ReportSpec...)
	}
	if len(input.ReportFill) > 0 {
		out.ReportFill = append([]byte{}, input.ReportFill...)
	}
	if len(input.ReportPrint) > 0 {
		out.ReportPrint = append([]byte{}, input.ReportPrint...)
	}
	if len(input.Diagnostics) > 0 {
		out.Diagnostics = append([]byte{}, input.Diagnostics...)
	}
	if input.StartedAt != nil {
		startedAt := *input.StartedAt
		out.StartedAt = &startedAt
	}
	if input.CompletedAt != nil {
		completedAt := *input.CompletedAt
		out.CompletedAt = &completedAt
	}
	return &out
}

func cloneArtifact(input *reportartifact.Record) *reportartifact.Record {
	if input == nil {
		return nil
	}
	out := *input
	if len(input.Data) > 0 {
		out.Data = append([]byte{}, input.Data...)
	}
	return &out
}
