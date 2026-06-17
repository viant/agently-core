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
	return writeJSONAtomic(path, job)
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
	return writeJSONAtomic(path, artifact)
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
