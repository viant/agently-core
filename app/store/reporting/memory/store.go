package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
)

var errNotFound = errors.New("reporting memory store: not found")

// Store is an in-memory reporting persistence implementation.
type Store struct {
	mu              sync.RWMutex
	jobs            map[string]*reportjob.Record
	artifacts       map[string]*reportartifact.Record
	sharedArtifacts map[string]*reportshareartifact.Record
}

// New constructs an empty in-memory reporting store.
func New() reportstore.Client {
	return &Store{
		jobs:            map[string]*reportjob.Record{},
		artifacts:       map[string]*reportartifact.Record{},
		sharedArtifacts: map[string]*reportshareartifact.Record{},
	}
}

// CreateJob persists a new job.
func (s *Store) CreateJob(_ context.Context, job *reportjob.Record) error {
	if s == nil || job == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.JobID]; exists {
		return fmt.Errorf("reporting memory store: job %s already exists: %w", job.JobID, reportstore.ErrAlreadyExists)
	}
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

// ListJobs returns cloned stored jobs in unspecified order.
func (s *Store) ListJobs(_ context.Context) ([]*reportjob.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*reportjob.Record, 0, len(s.jobs))
	for _, job := range s.jobs {
		result = append(result, cloneJob(job))
	}
	return result, nil
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
	if _, exists := s.artifacts[artifact.ArtifactID]; exists {
		return fmt.Errorf("reporting memory store: artifact %s already exists: %w", artifact.ArtifactID, reportstore.ErrAlreadyExists)
	}
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

// ListArtifacts returns cloned stored artifacts in unspecified order.
func (s *Store) ListArtifacts(_ context.Context) ([]*reportartifact.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*reportartifact.Record, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		result = append(result, cloneArtifact(artifact))
	}
	return result, nil
}

// CreateSharedArtifact persists a new shared reporting artifact.
func (s *Store) CreateSharedArtifact(_ context.Context, artifact *reportshareartifact.Record) error {
	if s == nil || artifact == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sharedArtifacts[artifact.ArtifactID]; exists {
		return fmt.Errorf("reporting memory store: shared artifact %s already exists: %w", artifact.ArtifactID, reportstore.ErrAlreadyExists)
	}
	s.sharedArtifacts[artifact.ArtifactID] = cloneSharedArtifact(artifact)
	return nil
}

// GetSharedArtifact loads a stored shared reporting artifact.
func (s *Store) GetSharedArtifact(_ context.Context, artifactID string) (*reportshareartifact.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.sharedArtifacts[artifactID]
	if !ok {
		return nil, errNotFound
	}
	return cloneSharedArtifact(artifact), nil
}

// ListSharedArtifacts returns cloned shared reporting artifacts in unspecified
// order.
func (s *Store) ListSharedArtifacts(_ context.Context) ([]*reportshareartifact.Record, error) {
	if s == nil {
		return nil, errNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*reportshareartifact.Record, 0, len(s.sharedArtifacts))
	for _, artifact := range s.sharedArtifacts {
		result = append(result, cloneSharedArtifact(artifact))
	}
	return result, nil
}

// UpdateSharedArtifact replaces a stored shared reporting artifact.
func (s *Store) UpdateSharedArtifact(_ context.Context, artifact *reportshareartifact.Record) error {
	if s == nil || artifact == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sharedArtifacts[artifact.ArtifactID]; !ok {
		return errNotFound
	}
	s.sharedArtifacts[artifact.ArtifactID] = cloneSharedArtifact(artifact)
	return nil
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

func cloneSharedArtifact(input *reportshareartifact.Record) *reportshareartifact.Record {
	if input == nil {
		return nil
	}
	out := *input
	if len(input.Document) > 0 {
		out.Document = append([]byte{}, input.Document...)
	}
	if len(input.ReportSpec) > 0 {
		out.ReportSpec = append([]byte{}, input.ReportSpec...)
	}
	if len(input.ReportFill) > 0 {
		out.ReportFill = append([]byte{}, input.ReportFill...)
	}
	if len(input.ReportPrint) > 0 {
		out.ReportPrint = append([]byte{}, input.ReportPrint...)
	}
	if len(input.SavedViewOverlay) > 0 {
		out.SavedViewOverlay = append([]byte{}, input.SavedViewOverlay...)
	}
	if len(input.Metadata) > 0 {
		out.Metadata = append([]byte{}, input.Metadata...)
	}
	if input.UpdatedAt != nil {
		updatedAt := *input.UpdatedAt
		out.UpdatedAt = &updatedAt
	}
	return &out
}
