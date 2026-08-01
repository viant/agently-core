package memory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
)

var errNotFound = errors.New("reporting memory store: not found")

// Store is an in-memory reporting persistence implementation.
type Store struct {
	mu              sync.RWMutex
	jobs            map[string]*reportjob.Record
	artifacts       map[string]*reportartifact.Record
	sharedArtifacts map[string]*reportshareartifact.Record
	reportRuns      map[string]*reportrun.Record
	runRequests     map[string]string
	reportContexts  map[string]*reportcontext.Record
}

// New constructs an empty in-memory reporting store.
func New() reportstore.Client {
	return &Store{
		jobs:            map[string]*reportjob.Record{},
		artifacts:       map[string]*reportartifact.Record{},
		sharedArtifacts: map[string]*reportshareartifact.Record{},
		reportRuns:      map[string]*reportrun.Record{},
		runRequests:     map[string]string{},
		reportContexts:  map[string]*reportcontext.Record{},
	}
}

func ownerContextKey(ownerID, conversationID string) string {
	return strings.TrimSpace(ownerID) + "\x00" + strings.TrimSpace(conversationID)
}

func authenticatedOwner(ctx context.Context) string {
	return strings.TrimSpace(authctx.EffectiveUserID(ctx))
}

// CreateReportRun persists a new owner-scoped browser run.
func (s *Store) CreateReportRun(ctx context.Context, run *reportrun.Record) error {
	if s == nil || run == nil {
		return reportstore.ErrNotFound
	}
	ownerID := authenticatedOwner(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(run.OwnerID) {
		return reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reportRuns[run.ReportRunID]; ok {
		return reportstore.ErrAlreadyExists
	}
	requestKey := ownerContextKey(ownerID, run.UIRunRequestID)
	if _, ok := s.runRequests[requestKey]; ok {
		return reportstore.ErrAlreadyExists
	}
	s.reportRuns[run.ReportRunID] = cloneReportRun(run)
	s.runRequests[requestKey] = run.ReportRunID
	return nil
}

// GetReportRun returns an authenticated owner's run.
func (s *Store) GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error) {
	ownerID := authenticatedOwner(ctx)
	if s == nil || ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.reportRuns[strings.TrimSpace(reportRunID)]
	if !ok || strings.TrimSpace(run.OwnerID) != ownerID {
		return nil, reportstore.ErrNotFound
	}
	return cloneReportRun(run), nil
}

// GetReportRunByRequestID resolves transport retries without creating a
// second run.
func (s *Store) GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error) {
	ownerID := authenticatedOwner(ctx)
	if s == nil || ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	reportRunID, ok := s.runRequests[ownerContextKey(ownerID, uiRunRequestID)]
	if !ok {
		return nil, reportstore.ErrNotFound
	}
	run, ok := s.reportRuns[reportRunID]
	if !ok || strings.TrimSpace(run.OwnerID) != ownerID {
		return nil, reportstore.ErrNotFound
	}
	return cloneReportRun(run), nil
}

// UpdateReportRunCAS replaces a run only at the expected revision.
func (s *Store) UpdateReportRunCAS(ctx context.Context, run *reportrun.Record, expectedRevision int64) error {
	ownerID := authenticatedOwner(ctx)
	if s == nil || run == nil || ownerID == "" || ownerID != strings.TrimSpace(run.OwnerID) {
		return reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.reportRuns[strings.TrimSpace(run.ReportRunID)]
	if !ok || strings.TrimSpace(current.OwnerID) != ownerID {
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
	s.reportRuns[run.ReportRunID] = cloneReportRun(run)
	return nil
}

// GetConversationReportContext loads an active pointer for the current owner.
func (s *Store) GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	ownerID := authenticatedOwner(ctx)
	if s == nil || ownerID == "" {
		return nil, reportstore.ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.reportContexts[ownerContextKey(ownerID, conversationID)]
	if !ok {
		return nil, reportstore.ErrNotFound
	}
	return cloneReportContext(record), nil
}

// PutConversationReportContextCAS creates revision one from expected zero, or
// replaces an existing pointer at its exact expected revision.
func (s *Store) PutConversationReportContextCAS(ctx context.Context, record *reportcontext.Record, expectedRevision int64) error {
	ownerID := authenticatedOwner(ctx)
	if s == nil || record == nil || ownerID == "" || ownerID != strings.TrimSpace(record.OwnerID) {
		return reportstore.ErrNotFound
	}
	key := ownerContextKey(ownerID, record.ConversationID)
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.reportContexts[key]
	if !exists {
		if expectedRevision != 0 {
			return reportstore.ErrCASMismatch
		}
		s.reportContexts[key] = cloneReportContext(record)
		return nil
	}
	if current.Revision != expectedRevision {
		return reportstore.ErrCASMismatch
	}
	s.reportContexts[key] = cloneReportContext(record)
	return nil
}

// AdoptReportRunAndContextCAS binds a completed manual snapshot and advances
// its active conversation pointer under one lock.
func (s *Store) AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error {
	ownerID := authenticatedOwner(ctx)
	if s == nil || run == nil || record == nil || ownerID == "" ||
		ownerID != strings.TrimSpace(run.OwnerID) || ownerID != strings.TrimSpace(record.OwnerID) {
		return reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.reportRuns[strings.TrimSpace(run.ReportRunID)]
	if !ok || strings.TrimSpace(current.OwnerID) != ownerID {
		return reportstore.ErrNotFound
	}
	if current.Revision != expectedRunRevision {
		return reportstore.ErrCASMismatch
	}
	key := ownerContextKey(ownerID, record.ConversationID)
	currentContext, exists := s.reportContexts[key]
	switch {
	case !exists && expectedContextRevision != 0:
		return reportstore.ErrCASMismatch
	case exists && currentContext.Revision != expectedContextRevision:
		return reportstore.ErrCASMismatch
	}
	if err := reportstore.ValidateAdoptionMutation(current, run, expectedRunRevision, record, expectedContextRevision); err != nil {
		return err
	}
	s.reportRuns[run.ReportRunID] = cloneReportRun(run)
	s.reportContexts[key] = cloneReportContext(record)
	return nil
}

// CreateJob persists a new job.
func (s *Store) CreateJob(_ context.Context, job *reportjob.Record) error {
	if s == nil || job == nil {
		return errNotFound
	}
	if reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.jobs[job.JobID]; exists {
		return fmt.Errorf("reporting memory store: job %s already exists: %w", job.JobID, reportstore.ErrAlreadyExists)
	}
	s.jobs[job.JobID] = cloneJob(job)
	return nil
}

func (s *Store) SubmitJobFromRun(ctx context.Context, candidate *reportjob.Record) (*reportjob.Record, bool, error) {
	ownerID := authenticatedOwner(ctx)
	if s == nil || candidate == nil || ownerID == "" || ownerID != strings.TrimSpace(candidate.OwnerID) {
		return nil, false, reportstore.ErrNotFound
	}
	if err := reportstore.ValidateRunExportCandidate(candidate); err != nil {
		return nil, false, err
	}
	requestID := strings.TrimSpace(candidate.ExportRequestID)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.jobs {
		if strings.TrimSpace(existing.OwnerID) != ownerID ||
			strings.TrimSpace(existing.ConversationID) != strings.TrimSpace(candidate.ConversationID) ||
			strings.TrimSpace(existing.ExportRequestID) != requestID {
			continue
		}
		if sameRunExportRequest(existing, candidate) {
			return cloneJob(existing), true, nil
		}
		return nil, false, reportstore.ErrConflict
	}
	run, ok := s.reportRuns[strings.TrimSpace(candidate.ReportRunID)]
	if !ok || strings.TrimSpace(run.OwnerID) != ownerID {
		return nil, false, reportstore.ErrNotFound
	}
	if run.Status != reportrun.StatusCompleted || run.Revision < 1 {
		return nil, false, reportstore.ErrInvalidTransition
	}
	if len(bytes.TrimSpace(run.ReportSpec)) == 0 || len(bytes.TrimSpace(run.ReportFill)) == 0 || len(bytes.TrimSpace(run.ReportPrint)) == 0 {
		return nil, false, reportstore.ErrInvalidTransition
	}
	if runConversation := strings.TrimSpace(run.ConversationID); runConversation == "" ||
		runConversation != strings.TrimSpace(candidate.ConversationID) {
		return nil, false, reportstore.ErrNotFound
	}
	if _, exists := s.jobs[candidate.JobID]; exists {
		return nil, false, reportstore.ErrAlreadyExists
	}
	job := cloneJob(candidate)
	job.OwnerID = ownerID
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
	s.jobs[job.JobID] = cloneJob(job)
	return cloneJob(job), false, nil
}

func (s *Store) ClaimJob(_ context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error) {
	if s == nil {
		return nil, reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return nil, reportstore.ErrNotFound
	}
	if strings.TrimSpace(job.Status) != "queued" {
		return nil, reportstore.ErrInvalidTransition
	}
	job = cloneJob(job)
	job.Status = "running"
	startedAt = startedAt.UTC()
	job.StartedAt = &startedAt
	s.jobs[job.JobID] = job
	return cloneJob(job), nil
}

func (s *Store) CompleteJobWithArtifact(_ context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error) {
	if s == nil || artifact == nil {
		return nil, reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return nil, reportstore.ErrNotFound
	}
	if strings.TrimSpace(job.Status) == "succeeded" && strings.TrimSpace(job.ArtifactID) != "" {
		if existing, ok := s.artifacts[job.ArtifactID]; ok && sameArtifactJob(existing, job) {
			return cloneJob(job), nil
		}
		return nil, reportstore.ErrInvalidTransition
	}
	if strings.TrimSpace(job.Status) != "running" {
		return nil, reportstore.ErrInvalidTransition
	}
	for _, existing := range s.artifacts {
		if strings.TrimSpace(existing.JobID) == strings.TrimSpace(job.JobID) {
			if !sameArtifactJob(existing, job) {
				return nil, reportstore.ErrConflict
			}
			return s.completeJobFromArtifactLocked(job, existing, diagnostics, completedAt, retentionTTL), nil
		}
	}
	if _, exists := s.artifacts[artifact.ArtifactID]; exists {
		return nil, reportstore.ErrAlreadyExists
	}
	nextArtifact := cloneArtifact(artifact)
	nextArtifact.JobID = job.JobID
	nextArtifact.OwnerID = job.OwnerID
	nextArtifact.ArtifactRef = job.ArtifactRef
	nextArtifact.Format = job.Format
	s.artifacts[nextArtifact.ArtifactID] = nextArtifact
	return s.completeJobFromArtifactLocked(job, nextArtifact, diagnostics, completedAt, retentionTTL), nil
}

func (s *Store) completeJobFromArtifactLocked(job *reportjob.Record, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) *reportjob.Record {
	next := cloneJob(job)
	next.Status = "succeeded"
	next.ArtifactID = strings.TrimSpace(artifact.ArtifactID)
	next.Error = ""
	next.Diagnostics = append([]byte(nil), diagnostics...)
	completedAt = completedAt.UTC()
	next.CompletedAt = &completedAt
	next.RetentionTTL = retentionTTL
	s.jobs[next.JobID] = next
	return cloneJob(next)
}

func (s *Store) FailJob(_ context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error) {
	if s == nil {
		return nil, reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return nil, reportstore.ErrNotFound
	}
	if strings.TrimSpace(job.Status) != "running" {
		return nil, reportstore.ErrInvalidTransition
	}
	next := cloneJob(job)
	next.Status = "failed"
	next.Error = strings.TrimSpace(errorText)
	next.Diagnostics = append([]byte(nil), diagnostics...)
	completedAt = completedAt.UTC()
	next.CompletedAt = &completedAt
	s.jobs[next.JobID] = next
	return cloneJob(next), nil
}

func (s *Store) ReconcileRunningJobs(_ context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjob.Record, error) {
	if s == nil {
		return nil, reportstore.ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := []*reportjob.Record{}
	for id, current := range s.jobs {
		if strings.TrimSpace(current.Status) != "running" ||
			(current.StartedAt != nil && current.StartedAt.After(staleBefore)) {
			continue
		}
		var persistedArtifact *reportartifact.Record
		for _, candidate := range s.artifacts {
			if strings.TrimSpace(candidate.JobID) == strings.TrimSpace(current.JobID) {
				persistedArtifact = candidate
				break
			}
		}
		if persistedArtifact != nil {
			if !sameArtifactJob(persistedArtifact, current) {
				return nil, reportstore.ErrConflict
			}
			result = append(result, s.completeJobFromArtifactLocked(current, persistedArtifact, current.Diagnostics, reconciledAt, current.RetentionTTL))
			continue
		}
		next := cloneJob(current)
		next.Status = "failed"
		next.Error = strings.TrimSpace(errorText)
		reconciledAtUTC := reconciledAt.UTC()
		next.CompletedAt = &reconciledAtUTC
		s.jobs[id] = next
		result = append(result, cloneJob(next))
	}
	return result, nil
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
	current, ok := s.jobs[job.JobID]
	if !ok {
		return errNotFound
	}
	if reportstore.HasRunExportLink(current) || reportstore.HasRunExportLink(job) {
		return reportstore.ErrInvalidTransition
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
	job, exists := s.jobs[strings.TrimSpace(artifact.JobID)]
	if !exists {
		return reportstore.ErrNotFound
	}
	if !sameArtifactJob(artifact, job) {
		return reportstore.ErrConflict
	}
	for _, existing := range s.artifacts {
		if strings.TrimSpace(existing.JobID) == strings.TrimSpace(artifact.JobID) {
			return fmt.Errorf("reporting memory store: job %s already has an artifact: %w", artifact.JobID, reportstore.ErrAlreadyExists)
		}
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

// DeleteSharedArtifact removes a persisted shared reporting artifact.
func (s *Store) DeleteSharedArtifact(_ context.Context, artifactID string) error {
	if s == nil {
		return errNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sharedArtifacts[artifactID]; !ok {
		return errNotFound
	}
	delete(s.sharedArtifacts, artifactID)
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
	if len(input.Metadata) > 0 {
		out.Metadata = append([]byte{}, input.Metadata...)
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

func cloneReportRun(input *reportrun.Record) *reportrun.Record {
	if input == nil {
		return nil
	}
	out := *input
	out.RequestedParams = append([]byte(nil), input.RequestedParams...)
	out.EffectiveParams = append([]byte(nil), input.EffectiveParams...)
	out.ReportSpec = append([]byte(nil), input.ReportSpec...)
	out.ReportFill = append([]byte(nil), input.ReportFill...)
	out.ReportPrint = append([]byte(nil), input.ReportPrint...)
	if input.CompletedAt != nil {
		value := *input.CompletedAt
		out.CompletedAt = &value
	}
	return &out
}

func cloneReportContext(input *reportcontext.Record) *reportcontext.Record {
	if input == nil {
		return nil
	}
	out := *input
	return &out
}
