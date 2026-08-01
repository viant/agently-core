package fs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	authctx "github.com/viant/agently-core/internal/auth"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	"github.com/viant/agently-core/workspace"
)

var errNotFound = errors.New("reporting fs store: not found")
var reportingStateLocks sync.Map

// Store is a StateStore-backed durable reporting store.
type Store struct {
	stateStore workspace.StateStore
	mu         sync.Mutex
}

// New constructs a filesystem-backed reporting store using a workspace
// StateStore.
func New(stateStore workspace.StateStore) reportstore.Client {
	return &Store{stateStore: stateStore}
}

// CreateReportRun persists a new browser run and enforces owner-scoped
// uiRunRequestId uniqueness.
func (s *Store) CreateReportRun(ctx context.Context, run *reportrun.Record) error {
	if run == nil || !s.authenticates(ctx, run.OwnerID) {
		return reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.getReportRunByRequestID(ctx, run.UIRunRequestID); err == nil && existing != nil {
		return reportstore.ErrAlreadyExists
	} else if err != nil && !errors.Is(err, reportstore.ErrNotFound) {
		return err
	}
	path, err := s.recordPath(ctx, "runs", run.ReportRunID)
	if err != nil {
		return err
	}
	return writeJSONCreateOnly(path, run, "reporting fs store: run %s already exists: %w", strings.TrimSpace(run.ReportRunID), reportstore.ErrAlreadyExists)
}

// GetReportRun loads an authenticated owner's browser run.
func (s *Store) GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error) {
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.recordPath(ctx, "runs", reportRunID)
	if err != nil {
		return nil, err
	}
	record := &reportrun.Record{}
	if err := readJSON(path, record); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, reportstore.ErrNotFound
		}
		return nil, err
	}
	if !s.authenticates(ctx, record.OwnerID) {
		return nil, reportstore.ErrNotFound
	}
	return record, nil
}

// GetReportRunByRequestID resolves an authenticated transport retry.
func (s *Store) GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error) {
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getReportRunByRequestID(ctx, uiRunRequestID)
}

func (s *Store) getReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error) {
	ownerID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	requestID := strings.TrimSpace(uiRunRequestID)
	if ownerID == "" || requestID == "" || s == nil || s.stateStore == nil {
		return nil, reportstore.ErrNotFound
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", "runs"))
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, reportstore.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		record := &reportrun.Record{}
		if err := readJSON(filepath.Join(dir, entry.Name()), record); err != nil {
			return nil, err
		}
		if strings.TrimSpace(record.OwnerID) == ownerID && strings.TrimSpace(record.UIRunRequestID) == requestID {
			return record, nil
		}
	}
	return nil, reportstore.ErrNotFound
}

// UpdateReportRunCAS atomically replaces one file after revision validation.
func (s *Store) UpdateReportRunCAS(ctx context.Context, run *reportrun.Record, expectedRevision int64) error {
	if run == nil || !s.authenticates(ctx, run.OwnerID) {
		return reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.recordPath(ctx, "runs", run.ReportRunID)
	if err != nil {
		return err
	}
	current := &reportrun.Record{}
	if err := readJSON(path, current); err != nil {
		if errors.Is(err, errNotFound) {
			return reportstore.ErrNotFound
		}
		return err
	}
	if !s.authenticates(ctx, current.OwnerID) {
		return reportstore.ErrNotFound
	}
	if current.Revision != expectedRevision {
		return reportstore.ErrCASMismatch
	}
	if err := reportstore.ValidateReportRunUpdate(current, run, expectedRevision); err != nil {
		return err
	}
	if strings.TrimSpace(current.UIRunRequestID) != strings.TrimSpace(run.UIRunRequestID) {
		return reportstore.ErrAlreadyExists
	}
	return writeJSONAtomic(path, run)
}

// GetConversationReportContext loads an owner+conversation active pointer.
func (s *Store) GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	ownerID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	if ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.contextPath(ctx, ownerID, conversationID)
	if err != nil {
		return nil, err
	}
	record := &reportcontext.Record{}
	if err := readJSON(path, record); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, reportstore.ErrNotFound
		}
		return nil, err
	}
	if strings.TrimSpace(record.OwnerID) != ownerID || strings.TrimSpace(record.ConversationID) != strings.TrimSpace(conversationID) {
		return nil, reportstore.ErrNotFound
	}
	return record, nil
}

// PutConversationReportContextCAS creates or updates an active pointer at an
// exact expected revision.
func (s *Store) PutConversationReportContextCAS(ctx context.Context, record *reportcontext.Record, expectedRevision int64) error {
	if record == nil || !s.authenticates(ctx, record.OwnerID) {
		return reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.contextPath(ctx, record.OwnerID, record.ConversationID)
	if err != nil {
		return err
	}
	current := &reportcontext.Record{}
	err = readJSON(path, current)
	switch {
	case errors.Is(err, errNotFound):
		if expectedRevision != 0 {
			return reportstore.ErrCASMismatch
		}
		return writeJSONCreateOnly(path, record, "reporting fs store: context already exists: %w", reportstore.ErrCASMismatch)
	case err != nil:
		return err
	case current.Revision != expectedRevision:
		return reportstore.ErrCASMismatch
	default:
		return writeJSONAtomic(path, record)
	}
}

// AdoptReportRunAndContextCAS validates both revisions before changing either
// file. The process-wide keyed lock also serializes separate Store instances
// backed by the same state directory.
func (s *Store) AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error {
	if run == nil || record == nil || !s.authenticates(ctx, run.OwnerID) || !s.authenticates(ctx, record.OwnerID) {
		return reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	runPath, err := s.recordPath(ctx, "runs", run.ReportRunID)
	if err != nil {
		return err
	}
	contextPath, err := s.contextPath(ctx, record.OwnerID, record.ConversationID)
	if err != nil {
		return err
	}
	current := &reportrun.Record{}
	if err := readJSON(runPath, current); err != nil {
		if errors.Is(err, errNotFound) {
			return reportstore.ErrNotFound
		}
		return err
	}
	if !s.authenticates(ctx, current.OwnerID) {
		return reportstore.ErrNotFound
	}
	if current.Revision != expectedRunRevision {
		return reportstore.ErrCASMismatch
	}
	currentContext := &reportcontext.Record{}
	contextErr := readJSON(contextPath, currentContext)
	switch {
	case errors.Is(contextErr, errNotFound) && expectedContextRevision != 0:
		return reportstore.ErrCASMismatch
	case contextErr != nil && !errors.Is(contextErr, errNotFound):
		return contextErr
	case contextErr == nil && currentContext.Revision != expectedContextRevision:
		return reportstore.ErrCASMismatch
	}
	if err := reportstore.ValidateAdoptionMutation(current, run, expectedRunRevision, record, expectedContextRevision); err != nil {
		return err
	}
	if err := writeJSONAtomic(runPath, run); err != nil {
		return err
	}
	if errors.Is(contextErr, errNotFound) {
		err = writeJSONCreateOnly(contextPath, record, "reporting fs store: context already exists: %w", reportstore.ErrCASMismatch)
	} else {
		err = writeJSONAtomic(contextPath, record)
	}
	if err == nil {
		return nil
	}
	if rollbackErr := writeJSONAtomic(runPath, current); rollbackErr != nil {
		return fmt.Errorf("reporting fs store: adoption context write failed: %v; run rollback failed: %w", err, rollbackErr)
	}
	return err
}

func (s *Store) reportingStateLock(ctx context.Context) (*sync.Mutex, error) {
	if s == nil || s.stateStore == nil {
		return nil, errors.New("reporting fs store: state store is required")
	}
	root, err := s.stateStore.StatePath(ctx, "reporting")
	if err != nil {
		return nil, err
	}
	value, _ := reportingStateLocks.LoadOrStore(filepath.Clean(root), &sync.Mutex{})
	return value.(*sync.Mutex), nil
}

func (s *Store) authenticates(ctx context.Context, ownerID string) bool {
	return strings.TrimSpace(ownerID) != "" && strings.TrimSpace(authctx.EffectiveUserID(ctx)) == strings.TrimSpace(ownerID)
}

func (s *Store) contextPath(ctx context.Context, ownerID, conversationID string) (string, error) {
	ownerID = strings.TrimSpace(ownerID)
	conversationID = strings.TrimSpace(conversationID)
	if ownerID == "" || conversationID == "" {
		return "", reportstore.ErrNotFound
	}
	key := base64.RawURLEncoding.EncodeToString([]byte(ownerID + "\x00" + conversationID))
	return s.recordPath(ctx, "conversation_contexts", key)
}

// CreateJob persists a new export job.
func (s *Store) CreateJob(ctx context.Context, job *reportjob.Record) error {
	if job == nil {
		return errNotFound
	}
	if reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
	}
	path, err := s.recordPath(ctx, "jobs", job.JobID)
	if err != nil {
		return err
	}
	return writeJSONCreateOnly(path, job, "reporting fs store: job %s already exists: %w", strings.TrimSpace(job.JobID), reportstore.ErrAlreadyExists)
}

func (s *Store) SubmitJobFromRun(ctx context.Context, candidate *reportjob.Record) (*reportjob.Record, bool, error) {
	if candidate == nil || !s.authenticates(ctx, candidate.OwnerID) {
		return nil, false, reportstore.ErrNotFound
	}
	if err := reportstore.ValidateRunExportCandidate(candidate); err != nil {
		return nil, false, err
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, false, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, existing := range jobs {
		if strings.TrimSpace(existing.OwnerID) != strings.TrimSpace(candidate.OwnerID) ||
			strings.TrimSpace(existing.ConversationID) != strings.TrimSpace(candidate.ConversationID) ||
			strings.TrimSpace(existing.ExportRequestID) != strings.TrimSpace(candidate.ExportRequestID) {
			continue
		}
		if sameRunExportRequest(existing, candidate) {
			return cloneJob(existing), true, nil
		}
		return nil, false, reportstore.ErrConflict
	}
	runPath, err := s.recordPath(ctx, "runs", candidate.ReportRunID)
	if err != nil {
		return nil, false, err
	}
	run := &reportrun.Record{}
	if err := readJSON(runPath, run); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, false, reportstore.ErrNotFound
		}
		return nil, false, err
	}
	if !s.authenticates(ctx, run.OwnerID) {
		return nil, false, reportstore.ErrNotFound
	}
	if run.Status != reportrun.StatusCompleted || run.Revision < 1 ||
		len(bytes.TrimSpace(run.ReportSpec)) == 0 || len(bytes.TrimSpace(run.ReportFill)) == 0 || len(bytes.TrimSpace(run.ReportPrint)) == 0 {
		return nil, false, reportstore.ErrInvalidTransition
	}
	if runConversation := strings.TrimSpace(run.ConversationID); runConversation == "" ||
		runConversation != strings.TrimSpace(candidate.ConversationID) {
		return nil, false, reportstore.ErrNotFound
	}
	job := cloneJob(candidate)
	job.ConversationID = strings.TrimSpace(run.ConversationID)
	job.WorkspaceID = ""
	job.ArtifactRef = "report-run://" + strings.TrimSpace(run.ReportRunID)
	job.ReportRunID = strings.TrimSpace(run.ReportRunID)
	job.ReportRunRevision = run.Revision
	job.ReportSpec = append([]byte(nil), run.ReportSpec...)
	job.ReportFill = append([]byte(nil), run.ReportFill...)
	job.ReportPrint = append([]byte(nil), run.ReportPrint...)
	job.Metadata = nil
	job.Status = "queued"
	path, err := s.recordPath(ctx, "jobs", job.JobID)
	if err != nil {
		return nil, false, err
	}
	if err := writeJSONCreateOnly(path, job, "reporting fs store: job %s already exists: %w", strings.TrimSpace(job.JobID), reportstore.ErrAlreadyExists); err != nil {
		return nil, false, err
	}
	return cloneJob(job), false, nil
}

func (s *Store) ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error) {
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.recordPath(ctx, "jobs", jobID)
	if err != nil {
		return nil, err
	}
	job := &reportjob.Record{}
	if err := readJSON(path, job); err != nil {
		return nil, translateNotFound(err)
	}
	if strings.TrimSpace(job.Status) != "queued" {
		return nil, reportstore.ErrInvalidTransition
	}
	job.Status = "running"
	startedAt = startedAt.UTC()
	job.StartedAt = &startedAt
	if err := writeJSONAtomic(path, job); err != nil {
		return nil, err
	}
	return cloneJob(job), nil
}

func (s *Store) CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error) {
	if artifact == nil {
		return nil, reportstore.ErrNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	jobPath, err := s.recordPath(ctx, "jobs", jobID)
	if err != nil {
		return nil, err
	}
	job := &reportjob.Record{}
	if err := readJSON(jobPath, job); err != nil {
		return nil, translateNotFound(err)
	}
	artifacts, err := s.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	for _, existing := range artifacts {
		if strings.TrimSpace(existing.JobID) != strings.TrimSpace(job.JobID) {
			continue
		}
		if !sameArtifactJob(existing, job) {
			return nil, reportstore.ErrConflict
		}
		if strings.TrimSpace(job.Status) == "running" {
			return s.completeJobFromArtifact(ctx, jobPath, job, existing, diagnostics, completedAt, retentionTTL)
		}
		if strings.TrimSpace(job.Status) == "succeeded" && strings.TrimSpace(job.ArtifactID) == strings.TrimSpace(existing.ArtifactID) {
			return cloneJob(job), nil
		}
		return nil, reportstore.ErrInvalidTransition
	}
	if strings.TrimSpace(job.Status) != "running" {
		return nil, reportstore.ErrInvalidTransition
	}
	nextArtifact := cloneArtifact(artifact)
	nextArtifact.JobID = job.JobID
	nextArtifact.OwnerID = job.OwnerID
	nextArtifact.ArtifactRef = job.ArtifactRef
	nextArtifact.Format = job.Format
	artifactPath, err := s.recordPath(ctx, "artifacts", nextArtifact.ArtifactID)
	if err != nil {
		return nil, err
	}
	if err := writeJSONCreateOnly(artifactPath, nextArtifact, "reporting fs store: artifact %s already exists: %w", strings.TrimSpace(nextArtifact.ArtifactID), reportstore.ErrAlreadyExists); err != nil {
		return nil, err
	}
	return s.completeJobFromArtifact(ctx, jobPath, job, nextArtifact, diagnostics, completedAt, retentionTTL)
}

func (s *Store) completeJobFromArtifact(_ context.Context, jobPath string, job *reportjob.Record, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error) {
	next := cloneJob(job)
	next.Status = "succeeded"
	next.ArtifactID = strings.TrimSpace(artifact.ArtifactID)
	next.Error = ""
	next.Diagnostics = append([]byte(nil), diagnostics...)
	completedAt = completedAt.UTC()
	next.CompletedAt = &completedAt
	next.RetentionTTL = retentionTTL
	if err := writeJSONAtomic(jobPath, next); err != nil {
		return nil, err
	}
	return cloneJob(next), nil
}

func (s *Store) FailJob(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error) {
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.recordPath(ctx, "jobs", jobID)
	if err != nil {
		return nil, err
	}
	job := &reportjob.Record{}
	if err := readJSON(path, job); err != nil {
		return nil, translateNotFound(err)
	}
	if strings.TrimSpace(job.Status) != "running" {
		return nil, reportstore.ErrInvalidTransition
	}
	job.Status = "failed"
	job.Error = strings.TrimSpace(errorText)
	job.Diagnostics = append([]byte(nil), diagnostics...)
	completedAt = completedAt.UTC()
	job.CompletedAt = &completedAt
	if err := writeJSONAtomic(path, job); err != nil {
		return nil, err
	}
	return cloneJob(job), nil
}

func (s *Store) ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjob.Record, error) {
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return nil, err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	artifactByJob := map[string]*reportartifact.Record{}
	for _, artifact := range artifacts {
		jobID := strings.TrimSpace(artifact.JobID)
		if prior := artifactByJob[jobID]; prior != nil && strings.TrimSpace(prior.ArtifactID) != strings.TrimSpace(artifact.ArtifactID) {
			return nil, reportstore.ErrConflict
		}
		artifactByJob[jobID] = artifact
	}
	result := []*reportjob.Record{}
	for _, job := range jobs {
		if strings.TrimSpace(job.Status) != "running" || (job.StartedAt != nil && job.StartedAt.After(staleBefore)) {
			continue
		}
		path, pathErr := s.recordPath(ctx, "jobs", job.JobID)
		if pathErr != nil {
			return nil, pathErr
		}
		if artifact := artifactByJob[strings.TrimSpace(job.JobID)]; artifact != nil {
			if !sameArtifactJob(artifact, job) {
				return nil, reportstore.ErrConflict
			}
			completed, completeErr := s.completeJobFromArtifact(ctx, path, job, artifact, job.Diagnostics, reconciledAt, job.RetentionTTL)
			if completeErr != nil {
				return nil, completeErr
			}
			result = append(result, completed)
			continue
		}
		next := cloneJob(job)
		next.Status = "failed"
		next.Error = strings.TrimSpace(errorText)
		reconciledAtUTC := reconciledAt.UTC()
		next.CompletedAt = &reconciledAtUTC
		if err := writeJSONAtomic(path, next); err != nil {
			return nil, err
		}
		result = append(result, cloneJob(next))
	}
	return result, nil
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
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.recordPath(ctx, "jobs", job.JobID)
	if err != nil {
		return err
	}
	current := &reportjob.Record{}
	if err := readJSON(path, current); err != nil {
		return translateNotFound(err)
	}
	if reportstore.HasRunExportLink(current) || reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
	}
	return writeJSONAtomic(path, job)
}

// PutArtifact persists an export artifact.
func (s *Store) PutArtifact(ctx context.Context, artifact *reportartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	stateLock, err := s.reportingStateLock(ctx)
	if err != nil {
		return err
	}
	stateLock.Lock()
	defer stateLock.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	jobPath, err := s.recordPath(ctx, "jobs", artifact.JobID)
	if err != nil {
		return err
	}
	job := &reportjob.Record{}
	if err := readJSON(jobPath, job); err != nil {
		return translateNotFound(err)
	}
	if !sameArtifactJob(artifact, job) {
		return reportstore.ErrConflict
	}
	artifacts, err := s.ListArtifacts(ctx)
	if err != nil {
		return err
	}
	for _, existing := range artifacts {
		if strings.TrimSpace(existing.ArtifactID) == strings.TrimSpace(artifact.ArtifactID) {
			return fmt.Errorf("reporting fs store: artifact %s already exists: %w", artifact.ArtifactID, reportstore.ErrAlreadyExists)
		}
		if strings.TrimSpace(existing.JobID) == strings.TrimSpace(artifact.JobID) {
			return fmt.Errorf("reporting fs store: job %s already has an artifact: %w", artifact.JobID, reportstore.ErrAlreadyExists)
		}
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

// DeleteSharedArtifact removes a persisted shared reporting artifact.
func (s *Store) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	path, err := s.recordPath(ctx, "shared_artifacts", artifactID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return nil
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

func translateNotFound(err error) error {
	if errors.Is(err, errNotFound) {
		return reportstore.ErrNotFound
	}
	return err
}

func cloneJob(input *reportjob.Record) *reportjob.Record {
	if input == nil {
		return nil
	}
	out := *input
	out.ReportSpec = append([]byte(nil), input.ReportSpec...)
	out.ReportFill = append([]byte(nil), input.ReportFill...)
	out.ReportPrint = append([]byte(nil), input.ReportPrint...)
	out.Metadata = append([]byte(nil), input.Metadata...)
	out.Diagnostics = append([]byte(nil), input.Diagnostics...)
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

func sameRunExportRequest(existing, candidate *reportjob.Record) bool {
	return existing != nil && candidate != nil &&
		strings.TrimSpace(existing.ReportRunID) == strings.TrimSpace(candidate.ReportRunID) &&
		strings.TrimSpace(existing.Format) == strings.TrimSpace(candidate.Format) &&
		strings.TrimSpace(existing.Scope) == strings.TrimSpace(candidate.Scope)
}

func sameArtifactJob(artifact *reportartifact.Record, job *reportjob.Record) bool {
	return artifact != nil && job != nil &&
		strings.TrimSpace(artifact.JobID) == strings.TrimSpace(job.JobID) &&
		strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(job.OwnerID) &&
		strings.TrimSpace(artifact.ArtifactRef) == strings.TrimSpace(job.ArtifactRef) &&
		strings.EqualFold(strings.TrimSpace(artifact.Format), strings.TrimSpace(job.Format))
}

func cloneArtifact(input *reportartifact.Record) *reportartifact.Record {
	if input == nil {
		return nil
	}
	out := *input
	out.Data = append([]byte(nil), input.Data...)
	return &out
}
