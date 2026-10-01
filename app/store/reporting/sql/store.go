package sql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportfs "github.com/viant/agently-core/app/store/reporting/fs"
	authctx "github.com/viant/agently-core/internal/auth"
	reportaudit "github.com/viant/agently-core/internal/store/reportaudit"
	contextstore "github.com/viant/agently-core/internal/store/reporting/context"
	runstore "github.com/viant/agently-core/internal/store/reporting/run"
	sharedartifact "github.com/viant/agently-core/internal/store/reporting/sharedartifact"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportcontext "github.com/viant/agently-core/pkg/agently/reportcontext"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportrun "github.com/viant/agently-core/pkg/agently/reportrun"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
	"github.com/viant/agently-core/workspace"
	dexec "github.com/viant/datly/exec"
)

var errNotFound = errors.New("reporting sql store: not found")

type internalAccessKey struct{}

func WithInternalAccess(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, internalAccessKey{}, true)
}

func hasInternalAccess(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(internalAccessKey{}).(bool)
	return allowed
}

type Store struct {
	native     dexec.ComponentInvoker
	stateStore workspace.StateStore
	fallback   reportstore.Client
}

func New(ctx context.Context, invoker dexec.ComponentInvoker, stateStore workspace.StateStore, fallback reportstore.Client) (reportstore.Client, error) {
	if invoker == nil {
		return nil, errors.New("reporting native runtime is required")
	}
	value := reflect.ValueOf(invoker)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil, errors.New("reporting native runtime is required")
	}
	store := &Store{
		native:     invoker,
		stateStore: stateStore,
		fallback:   fallback,
	}
	if err := store.init(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) init(ctx context.Context) error {
	return s.importFilesystemState(ctx)
}

// CreateReportRun persists a new owner-scoped browser materialization.
func (s *Store) CreateReportRun(ctx context.Context, run *reportrun.Record) error {
	if run == nil {
		return reportstore.ErrNotFound
	}
	record := runstore.Record(*run)
	err := s.nativeRunStore().Create(ctx, &record)
	return mapNativeRunError(err)
}

// GetReportRun loads a browser run in the authenticated owner scope.
func (s *Store) GetReportRun(ctx context.Context, reportRunID string) (*reportrun.Record, error) {
	record, err := s.nativeRunStore().Get(ctx, reportRunID)
	if err != nil {
		return nil, mapNativeRunError(err)
	}
	result := reportrun.Record(*record)
	return &result, nil
}

// GetReportRunByRequestID resolves a transport retry in owner scope.
func (s *Store) GetReportRunByRequestID(ctx context.Context, uiRunRequestID string) (*reportrun.Record, error) {
	record, err := s.nativeRunStore().GetByRequestID(ctx, uiRunRequestID)
	if err != nil {
		return nil, mapNativeRunError(err)
	}
	result := reportrun.Record(*record)
	return &result, nil
}

// UpdateReportRunCAS replaces a run only when its owner and revision match.
func (s *Store) UpdateReportRunCAS(ctx context.Context, run *reportrun.Record, expectedRevision int64) error {
	if run == nil {
		return reportstore.ErrNotFound
	}
	record := runstore.Record(*run)
	return mapNativeRunError(s.nativeRunStore().UpdateCAS(ctx, &record, expectedRevision))
}

// AdoptReportRunAndContextCAS binds the completed manual run and advances the
// conversation pointer in one SQL transaction.
func (s *Store) AdoptReportRunAndContextCAS(ctx context.Context, run *reportrun.Record, expectedRunRevision int64, record *reportcontext.Record, expectedContextRevision int64) error {
	if run == nil || record == nil {
		return reportstore.ErrNotFound
	}
	if s.native == nil {
		return fmt.Errorf("native reporting runtime is required")
	}
	if owner := effectiveOwnerID(ctx); owner == "" || owner != strings.TrimSpace(run.OwnerID) || owner != strings.TrimSpace(record.OwnerID) {
		return reportstore.ErrNotFound
	}
	return s.adoptReportRunNative(ctx, run, expectedRunRevision, record, expectedContextRevision)
}

// GetConversationReportContext loads the active run pointer for an exact
// owner+conversation pair.
func (s *Store) GetConversationReportContext(ctx context.Context, conversationID string) (*reportcontext.Record, error) {
	record, err := s.nativeContextStore().Get(ctx, conversationID)
	if err != nil {
		return nil, mapNativeContextError(err)
	}
	result := reportcontext.Record(*record)
	return &result, nil
}

// PutConversationReportContextCAS creates revision one from expected zero or
// updates an exact existing revision.
func (s *Store) PutConversationReportContextCAS(ctx context.Context, record *reportcontext.Record, expectedRevision int64) error {
	if record == nil {
		return reportstore.ErrNotFound
	}
	nativeRecord := contextstore.Record(*record)
	return mapNativeContextError(s.nativeContextStore().PutCAS(ctx, &nativeRecord, expectedRevision))
}

func (s *Store) CreateJob(ctx context.Context, job *reportjob.Record) error {
	return s.createJobNative(ctx, job)
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*reportjob.Record, error) {
	return s.getJobNative(ctx, jobID)
}

func (s *Store) ListJobs(ctx context.Context) ([]*reportjob.Record, error) {
	return s.listJobsNative(ctx)
}

func (s *Store) SubmitJobFromRun(ctx context.Context, candidate *reportjob.Record) (*reportjob.Record, bool, error) {
	ownerID := effectiveOwnerID(ctx)
	if candidate == nil || ownerID == "" || ownerID != strings.TrimSpace(candidate.OwnerID) {
		return nil, false, reportstore.ErrNotFound
	}
	if s.native == nil {
		return nil, false, fmt.Errorf("native reporting runtime is required")
	}
	return s.submitJobNative(ctx, candidate)
}

func (s *Store) ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*reportjob.Record, error) {
	return s.claimJobNative(ctx, jobID, startedAt)
}

func (s *Store) CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *reportartifact.Record, diagnostics []byte, completedAt time.Time, retentionTTL time.Duration) (*reportjob.Record, error) {
	ownerID := effectiveOwnerID(ctx)
	internal := hasInternalAccess(ctx)
	if artifact == nil || (ownerID == "" && !internal) {
		return nil, reportstore.ErrNotFound
	}
	if s.native == nil {
		return nil, fmt.Errorf("native reporting runtime is required")
	}
	return s.completeJobNative(ctx, jobID, artifact, diagnostics, completedAt, retentionTTL, internal)
}

func (s *Store) FailJob(ctx context.Context, jobID, errorText string, diagnostics []byte, completedAt time.Time) (*reportjob.Record, error) {
	return s.failJobNative(ctx, jobID, errorText, diagnostics, completedAt)
}

func (s *Store) ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*reportjob.Record, error) {
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	artifactByJob := make(map[string]*reportartifact.Record, len(artifacts))
	for _, artifact := range artifacts {
		if artifact == nil {
			continue
		}
		jobID := strings.TrimSpace(artifact.JobID)
		if prior := artifactByJob[jobID]; prior != nil &&
			strings.TrimSpace(prior.ArtifactID) != strings.TrimSpace(artifact.ArtifactID) {
			return nil, reportstore.ErrConflict
		}
		artifactByJob[jobID] = artifact
	}
	result := []*reportjob.Record{}
	for _, job := range jobs {
		if job == nil || job.Status != "running" ||
			(job.StartedAt != nil && job.StartedAt.After(staleBefore)) {
			continue
		}
		if artifact := artifactByJob[strings.TrimSpace(job.JobID)]; artifact != nil {
			completed, completeErr := s.CompleteJobWithArtifact(
				ctx, job.JobID, artifact, job.Diagnostics, reconciledAt, job.RetentionTTL,
			)
			if errors.Is(completeErr, reportstore.ErrInvalidTransition) {
				continue
			}
			if completeErr != nil {
				return nil, completeErr
			}
			result = append(result, completed)
			continue
		}
		failed, failErr := s.FailJob(ctx, job.JobID, errorText, job.Diagnostics, reconciledAt)
		if errors.Is(failErr, reportstore.ErrInvalidTransition) {
			continue
		}
		if failErr != nil {
			return nil, failErr
		}
		result = append(result, failed)
	}
	return result, nil
}

func (s *Store) UpdateJob(ctx context.Context, job *reportjob.Record) error {
	return s.updateJobNative(ctx, job)
}

func (s *Store) PutArtifact(ctx context.Context, artifact *reportartifact.Record) error {
	return s.putArtifactNative(ctx, artifact)
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error) {
	return s.getArtifactNative(ctx, artifactID)
}

func (s *Store) ListArtifacts(ctx context.Context) ([]*reportartifact.Record, error) {
	return s.listArtifactsNative(ctx)
}

func (s *Store) CreateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(artifact.OwnerID) {
		return errNotFound
	}
	existing, err := s.GetSharedArtifact(ctx, artifact.ArtifactID)
	switch {
	case err == nil && existing != nil:
		return fmt.Errorf("reporting sql store: shared artifact %s already exists: %w", strings.TrimSpace(artifact.ArtifactID), reportstore.ErrAlreadyExists)
	case err != nil && !errors.Is(err, errNotFound):
		return err
	}
	return s.sharedArtifactStore().Create(ctx, toNativeSharedArtifact(artifact))
}

func (s *Store) GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifact.Record, error) {
	record, err := s.sharedArtifactStore().Get(ctx, artifactID)
	if errors.Is(err, sharedartifact.ErrNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return fromNativeSharedArtifact(record), nil
}

func (s *Store) ListSharedArtifacts(ctx context.Context) ([]*reportshareartifact.Record, error) {
	records, err := s.sharedArtifactStore().List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*reportshareartifact.Record, 0, len(records))
	for _, record := range records {
		result = append(result, fromNativeSharedArtifact(record))
	}
	return result, nil
}

func (s *Store) UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	ownerID := effectiveOwnerID(ctx)
	if ownerID == "" || ownerID != strings.TrimSpace(artifact.OwnerID) {
		return errNotFound
	}
	current, err := s.GetSharedArtifact(ctx, artifact.ArtifactID)
	if err != nil {
		return err
	}
	if current == nil || strings.TrimSpace(current.OwnerID) != ownerID {
		return errNotFound
	}
	err = s.sharedArtifactStore().Update(ctx, toNativeSharedArtifact(artifact))
	if errors.Is(err, sharedartifact.ErrNotFound) {
		return errNotFound
	}
	return err
}

// DeleteSharedArtifact removes an owned shared reporting artifact.
func (s *Store) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	err := s.sharedArtifactStore().Delete(ctx, artifactID)
	if errors.Is(err, sharedartifact.ErrNotFound) {
		return errNotFound
	}
	return err
}

func (s *Store) sharedArtifactStore() *sharedartifact.Store {
	return &sharedartifact.Store{Invoker: s.native, OwnerID: effectiveOwnerID}
}

func toNativeSharedArtifact(input *reportshareartifact.Record) *sharedartifact.Record {
	if input == nil {
		return nil
	}
	record := sharedartifact.Record(*input)
	return &record
}

func fromNativeSharedArtifact(input *sharedartifact.Record) *reportshareartifact.Record {
	if input == nil {
		return nil
	}
	record := reportshareartifact.Record(*input)
	return &record
}

func (s *Store) importFilesystemState(ctx context.Context) error {
	if s.fallback == nil {
		return nil
	}
	if err := s.importSharedArtifacts(ctx); err != nil {
		return err
	}
	if err := s.importJobs(ctx); err != nil {
		return err
	}
	if err := s.importArtifacts(ctx); err != nil {
		return err
	}
	return s.importAudits(ctx)
}

func (s *Store) importSharedArtifacts(ctx context.Context) error {
	items, err := s.fallback.ListSharedArtifacts(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		trusted := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: item.OwnerID})
		store := s.sharedArtifactStore()
		_, err := store.Get(trusted, item.ArtifactID)
		switch {
		case err == nil:
			continue
		case errors.Is(err, sharedartifact.ErrNotFound):
			if err := store.Create(trusted, toNativeSharedArtifact(item)); err != nil && !errors.Is(err, sharedartifact.ErrAlreadyExists) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func (s *Store) importJobs(ctx context.Context) error {
	items, err := s.fallback.ListJobs(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		trusted := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: item.OwnerID})
		_, err := s.GetJob(trusted, item.JobID)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errNotFound):
			if err := s.CreateJob(trusted, item); err != nil && !errors.Is(err, reportstore.ErrAlreadyExists) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func (s *Store) importArtifacts(ctx context.Context) error {
	items, err := s.fallback.ListArtifacts(ctx)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item == nil {
			continue
		}
		trusted := authctx.WithUserInfo(ctx, &authctx.UserInfo{Subject: item.OwnerID})
		_, err := s.GetArtifact(trusted, item.ArtifactID)
		switch {
		case err == nil:
			continue
		case errors.Is(err, errNotFound):
			if err := s.PutArtifact(trusted, item); err != nil && !errors.Is(err, reportstore.ErrAlreadyExists) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func (s *Store) importAudits(ctx context.Context) error {
	if s.stateStore == nil {
		return nil
	}
	items, err := reportfs.ListAuditEvents(ctx, s.stateStore)
	if err != nil {
		return nil
	}
	audit := &reportaudit.Store{Invoker: s.native}
	trusted := native.WithAccess(ctx, native.Access{Internal: true})
	for _, item := range items {
		if item == nil {
			continue
		}
		metadata, err := json.Marshal(item.Metadata)
		if err != nil {
			return err
		}
		eventID := fmt.Sprintf("%s_%s_%s_%s", item.EventType, item.ArtifactID, item.JobID, item.ActorID)
		if strings.Trim(eventID, "_") == "" {
			continue
		}
		if err := audit.ImportIfAbsent(trusted, reportaudit.Event{
			ID: eventID, Type: item.EventType, ArtifactRef: item.ArtifactRef,
			Version: int64(item.Version), JobID: item.JobID, ArtifactID: item.ArtifactID,
			ActorID: item.ActorID, ActorRef: item.ActorRef, OccurredAt: item.OccurredAt,
			MetadataJSON: metadata,
		}); err != nil {
			return err
		}
	}
	return nil
}

func effectiveOwnerID(ctx context.Context) string {
	return strings.TrimSpace(authctx.EffectiveUserID(ctx))
}
