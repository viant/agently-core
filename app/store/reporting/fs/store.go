package fs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	"github.com/viant/agently-core/workspace"
)

var errNotFound = errors.New("reporting fs store: not found")

// Store is a StateStore-backed durable reporting store.
type Store struct {
	stateStore workspace.StateStore
}

// New constructs a filesystem-backed reporting store using a workspace
// StateStore.
func New(stateStore workspace.StateStore) reportstore.Client {
	return &Store{stateStore: stateStore}
}

// CreateJob persists a new export job.
func (s *Store) CreateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	path, err := s.recordPath(ctx, "jobs", job.JobID)
	if err != nil {
		return err
	}
	return writeJSONCreateOnly(path, job, "reporting fs store: job %s already exists: %w", strings.TrimSpace(job.JobID), reportstore.ErrAlreadyExists)
}

// GetJob loads a persisted export job.
func (s *Store) GetJob(ctx context.Context, jobID string) (*reportjob.Record, error) {
	path, err := s.recordPath(ctx, "jobs", jobID)
	if err != nil {
		return nil, err
	}
	record := &reportjob.Record{}
	if err := readJSON(path, record); err != nil {
		return nil, err
	}
	return record, nil
}

// ListJobs loads all persisted export jobs in unspecified order.
func (s *Store) ListJobs(ctx context.Context) ([]*reportjob.Record, error) {
	if s == nil || s.stateStore == nil {
		return nil, fmt.Errorf("reporting fs store: state store is required")
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", "jobs"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*reportjob.Record{}, nil
		}
		return nil, err
	}
	result := make([]*reportjob.Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		record := &reportjob.Record{}
		if err := readJSON(filepath.Join(dir, name), record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// UpdateJob replaces a persisted export job.
func (s *Store) UpdateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	path, err := s.recordPath(ctx, "jobs", job.JobID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, job)
}

// PutArtifact persists an export artifact.
func (s *Store) PutArtifact(ctx context.Context, artifact *reportartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	path, err := s.recordPath(ctx, "artifacts", artifact.ArtifactID)
	if err != nil {
		return err
	}
	return writeJSONCreateOnly(path, artifact, "reporting fs store: artifact %s already exists: %w", strings.TrimSpace(artifact.ArtifactID), reportstore.ErrAlreadyExists)
}

// GetArtifact loads a persisted export artifact.
func (s *Store) GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error) {
	path, err := s.recordPath(ctx, "artifacts", artifactID)
	if err != nil {
		return nil, err
	}
	record := &reportartifact.Record{}
	if err := readJSON(path, record); err != nil {
		return nil, err
	}
	return record, nil
}

// ListArtifacts loads all persisted export artifacts in unspecified order.
func (s *Store) ListArtifacts(ctx context.Context) ([]*reportartifact.Record, error) {
	if s == nil || s.stateStore == nil {
		return nil, fmt.Errorf("reporting fs store: state store is required")
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", "artifacts"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*reportartifact.Record{}, nil
		}
		return nil, err
	}
	result := make([]*reportartifact.Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		record := &reportartifact.Record{}
		if err := readJSON(filepath.Join(dir, name), record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// CreateSharedArtifact persists a new shared reporting artifact.
func (s *Store) CreateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	path, err := s.recordPath(ctx, "shared_artifacts", artifact.ArtifactID)
	if err != nil {
		return err
	}
	return writeJSONCreateOnly(path, artifact, "reporting fs store: shared artifact %s already exists: %w", strings.TrimSpace(artifact.ArtifactID), reportstore.ErrAlreadyExists)
}

// GetSharedArtifact loads a persisted shared reporting artifact.
func (s *Store) GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifact.Record, error) {
	path, err := s.recordPath(ctx, "shared_artifacts", artifactID)
	if err != nil {
		return nil, err
	}
	record := &reportshareartifact.Record{}
	if err := readJSON(path, record); err != nil {
		return nil, err
	}
	return record, nil
}

// ListSharedArtifacts loads all persisted shared reporting artifacts in
// unspecified order.
func (s *Store) ListSharedArtifacts(ctx context.Context) ([]*reportshareartifact.Record, error) {
	if s == nil || s.stateStore == nil {
		return nil, fmt.Errorf("reporting fs store: state store is required")
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", "shared_artifacts"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []*reportshareartifact.Record{}, nil
		}
		return nil, err
	}
	result := make([]*reportshareartifact.Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		record := &reportshareartifact.Record{}
		if err := readJSON(filepath.Join(dir, name), record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// UpdateSharedArtifact replaces a persisted shared reporting artifact.
func (s *Store) UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	path, err := s.recordPath(ctx, "shared_artifacts", artifact.ArtifactID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, artifact)
}

func (s *Store) recordPath(ctx context.Context, category, id string) (string, error) {
	normalizedID := strings.TrimSpace(id)
	if normalizedID == "" || normalizedID != filepath.Base(normalizedID) || strings.Contains(normalizedID, "..") {
		return "", fmt.Errorf("reporting fs store: invalid id %q", id)
	}
	if s == nil || s.stateStore == nil {
		return "", fmt.Errorf("reporting fs store: state store is required")
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", category))
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, normalizedID+".json"), nil
}

func writeJSONAtomic(path string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".reporting-*.tmp")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func writeJSONCreateOnly(path string, value interface{}, duplicateFormat string, duplicateArgs ...interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf(duplicateFormat, duplicateArgs...)
		}
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Close()
}

func readJSON(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errNotFound
		}
		return err
	}
	return json.Unmarshal(data, target)
}
