package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	iauth "github.com/viant/agently-core/internal/auth"
	reportartifactmodel "github.com/viant/agently-core/model/reportartifact"
	reportjobmodel "github.com/viant/agently-core/model/reportjob"
	reportshareartifactmodel "github.com/viant/agently-core/model/reportshareartifact"
	identity "github.com/viant/agently-core/protocol/resource"
	forgeresource "github.com/viant/forge/backend/resource"
)

// NewStoreAdapter bridges the generic reporting store client into the
// reporting service's Store interface.
func NewStoreAdapter(client reportstore.Client) Store {
	if client == nil {
		return nil
	}
	return &storeAdapter{client: client}
}

type storeAdapter struct {
	client reportstore.Client
}

func (s *storeAdapter) CreateJob(ctx context.Context, job *ExportJob) error {
	return translateStoreError(s.client.CreateJob(ctx, encodeJob(job)))
}

func (s *storeAdapter) GetJob(ctx context.Context, jobID string) (*ExportJob, error) {
	job, err := s.client.GetJob(ctx, strings.TrimSpace(jobID))
	if err != nil {
		if isStoreNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decodeJob(job)
}

func (s *storeAdapter) ListJobs(ctx context.Context) ([]*ExportJob, error) {
	jobs, err := s.client.ListJobs(ctx)
	if err != nil {
		if isStoreNotFound(err) {
			return []*ExportJob{}, nil
		}
		return nil, err
	}
	result := make([]*ExportJob, 0, len(jobs))
	for _, job := range jobs {
		decoded, err := decodeJob(job)
		if err != nil {
			return nil, err
		}
		if decoded != nil {
			result = append(result, decoded)
		}
	}
	return result, nil
}

func (s *storeAdapter) UpdateJob(ctx context.Context, job *ExportJob) error {
	return translateStoreError(s.client.UpdateJob(ctx, encodeJob(job)))
}

func (s *storeAdapter) SubmitJobFromRun(ctx context.Context, candidate *ExportJob) (*ExportJob, bool, error) {
	client, ok := s.client.(reportstore.RunExportClient)
	if !ok {
		return nil, false, reportstore.ErrSchemaRequired
	}
	job, replay, err := client.SubmitJobFromRun(ctx, encodeJob(candidate))
	if err != nil {
		return nil, false, translateStoreError(err)
	}
	decoded, err := decodeJob(job)
	return decoded, replay, err
}

func (s *storeAdapter) ClaimJob(ctx context.Context, jobID string, startedAt time.Time) (*ExportJob, error) {
	client, ok := s.client.(reportstore.RunExportClient)
	if !ok {
		return nil, reportstore.ErrInvalidTransition
	}
	job, err := client.ClaimJob(ctx, strings.TrimSpace(jobID), startedAt)
	if err != nil {
		return nil, translateStoreError(err)
	}
	return decodeJob(job)
}

func (s *storeAdapter) CompleteJobWithArtifact(ctx context.Context, jobID string, artifact *Artifact, diagnostics []Diagnostic, completedAt time.Time, retentionTTL time.Duration) (*ExportJob, error) {
	client, ok := s.client.(reportstore.RunExportClient)
	if !ok {
		return nil, reportstore.ErrInvalidTransition
	}
	encodedDiagnostics, _ := json.Marshal(diagnostics)
	job, err := client.CompleteJobWithArtifact(ctx, strings.TrimSpace(jobID), encodeArtifact(artifact), encodedDiagnostics, completedAt, retentionTTL)
	if err != nil {
		return nil, translateStoreError(err)
	}
	return decodeJob(job)
}

func (s *storeAdapter) FailJob(ctx context.Context, jobID, errorText string, diagnostics []Diagnostic, completedAt time.Time) (*ExportJob, error) {
	client, ok := s.client.(reportstore.RunExportClient)
	if !ok {
		return nil, reportstore.ErrInvalidTransition
	}
	encodedDiagnostics, _ := json.Marshal(diagnostics)
	job, err := client.FailJob(ctx, strings.TrimSpace(jobID), strings.TrimSpace(errorText), encodedDiagnostics, completedAt)
	if err != nil {
		return nil, translateStoreError(err)
	}
	return decodeJob(job)
}

func (s *storeAdapter) ReconcileRunningJobs(ctx context.Context, staleBefore, reconciledAt time.Time, errorText string) ([]*ExportJob, error) {
	client, ok := s.client.(reportstore.RunExportClient)
	if !ok {
		return nil, reportstore.ErrInvalidTransition
	}
	jobs, err := client.ReconcileRunningJobs(ctx, staleBefore, reconciledAt, strings.TrimSpace(errorText))
	if err != nil {
		return nil, translateStoreError(err)
	}
	result := make([]*ExportJob, 0, len(jobs))
	for _, job := range jobs {
		decoded, decodeErr := decodeJob(job)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, decoded)
	}
	return result, nil
}

func (s *storeAdapter) PutArtifact(ctx context.Context, artifact *Artifact) error {
	return translateStoreError(s.client.PutArtifact(ctx, encodeArtifact(artifact)))
}

func (s *storeAdapter) GetArtifact(ctx context.Context, artifactID string) (*Artifact, error) {
	artifact, err := s.client.GetArtifact(ctx, strings.TrimSpace(artifactID))
	if err != nil {
		if isStoreNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decodeArtifact(artifact)
}

func (s *storeAdapter) ListArtifacts(ctx context.Context) ([]*Artifact, error) {
	artifacts, err := s.client.ListArtifacts(ctx)
	if err != nil {
		if isStoreNotFound(err) {
			return []*Artifact{}, nil
		}
		return nil, err
	}
	result := make([]*Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		decoded, err := decodeArtifact(artifact)
		if err != nil {
			return nil, err
		}
		if decoded != nil {
			result = append(result, decoded)
		}
	}
	return result, nil
}

func (s *storeAdapter) CreateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	row, err := encodeSharedArtifact(artifact)
	if err != nil {
		return err
	}
	return translateStoreError(s.client.CreateSharedArtifact(ctx, row))
}

func (s *storeAdapter) GetSharedArtifact(ctx context.Context, artifactID string) (*SharedArtifact, error) {
	artifact, err := s.client.GetSharedArtifact(ctx, strings.TrimSpace(artifactID))
	if err != nil {
		if isStoreNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return decodeSharedArtifact(artifact)
}

// SharedArtifactRead keeps an unreadable stored definition local to its row.
type SharedArtifactRead struct {
	Artifact *SharedArtifact
	Err      error
}

// ReadSharedArtifactBatch holds one metadata operation's rows. The final check
// compares the exact persisted columns, including records removed or replaced
// without changing their version, before releasing the page to its caller.
func (s *storeAdapter) ReadSharedArtifactBatch(ctx context.Context, ids []string) (map[string]SharedArtifactRead, func() error, error) {
	source, ok := s.client.(interface {
		GetSharedArtifacts(context.Context, []string) ([]*reportshareartifactmodel.Record, error)
	})
	if !ok {
		return nil, nil, nil
	}
	read := func() (map[string]*reportshareartifactmodel.Record, error) {
		rows, err := source.GetSharedArtifacts(ctx, ids)
		if err != nil {
			return nil, err
		}
		result := make(map[string]*reportshareartifactmodel.Record, len(rows))
		for _, row := range rows {
			if row != nil {
				result[row.ArtifactID] = row
			}
		}
		return result, nil
	}
	original, err := read()
	if err != nil {
		return nil, nil, err
	}
	decoded := make(map[string]SharedArtifactRead, len(original))
	for id, row := range original {
		artifact, err := decodeSharedArtifact(row)
		decoded[id] = SharedArtifactRead{Artifact: artifact, Err: err}
	}
	finish := func() error {
		current, err := read()
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, original) {
			return identity.ErrResourceStale
		}
		return nil
	}
	return decoded, finish, nil
}

// ListSharedArtifactHeaders returns discovery fields without decoding stored report content.
func (s *storeAdapter) ListSharedArtifactHeaders(ctx context.Context, offset, limit int) ([]*SharedArtifact, error) {
	if offset < 0 || limit <= 0 {
		return nil, identity.ErrResource
	}
	var rows []*reportshareartifactmodel.Record
	var err error
	if source, ok := s.client.(interface {
		ListSharedArtifactHeaders(context.Context, int, int) ([]*reportshareartifactmodel.Record, error)
	}); ok {
		rows, err = source.ListSharedArtifactHeaders(ctx, offset, limit)
	} else {
		rows, err = s.client.ListSharedArtifacts(ctx)
		if err == nil {
			visible := make([]*reportshareartifactmodel.Record, 0, len(rows))
			for _, row := range rows {
				if row != nil && row.OwnerID == iauth.EffectiveUserID(ctx) {
					visible = append(visible, row)
				}
			}
			rows = visible
			sort.SliceStable(rows, func(i, j int) bool {
				return rows[i].ArtifactID < rows[j].ArtifactID
			})
			start := min(offset, len(rows))
			rows = rows[start : start+min(limit, len(rows)-start)]
		}
	}
	if err != nil {
		return nil, err
	}
	result := make([]*SharedArtifact, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			result = append(result, &SharedArtifact{ArtifactID: row.ArtifactID, ArtifactRef: row.ArtifactRef, OwnerID: row.OwnerID, Title: row.Title, Kind: row.Kind, Lifecycle: row.Lifecycle, Version: row.Version})
		}
	}
	return result, nil
}

func (s *storeAdapter) ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error) {
	artifacts, err := s.client.ListSharedArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*SharedArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		decoded, err := decodeSharedArtifact(artifact)
		if err != nil {
			return nil, err
		}
		if decoded != nil {
			result = append(result, decoded)
		}
	}
	return result, nil
}

func (s *storeAdapter) UpdateSharedArtifact(ctx context.Context, artifact *SharedArtifact) error {
	row, err := encodeSharedArtifact(artifact)
	if err != nil {
		return err
	}
	return translateStoreError(s.client.UpdateSharedArtifact(ctx, row))
}

func (s *storeAdapter) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	return translateStoreError(s.client.DeleteSharedArtifact(ctx, strings.TrimSpace(artifactID)))
}

func encodeJob(input *ExportJob) *reportjobmodel.Record {
	if input == nil {
		return nil
	}
	var diagnostics []byte
	if len(input.Diagnostics) > 0 {
		diagnostics, _ = json.Marshal(input.Diagnostics)
	}
	metadata := encodeExportJobMetadata(input.Metadata, input.ResourcePin, input)
	return &reportjobmodel.Record{
		JobID:             strings.TrimSpace(input.JobID),
		ArtifactRef:       strings.TrimSpace(input.ArtifactRef),
		OwnerID:           strings.TrimSpace(input.OwnerID),
		ConversationID:    strings.TrimSpace(input.ConversationID),
		WorkspaceID:       strings.TrimSpace(input.WorkspaceID),
		AuthContextRef:    strings.TrimSpace(input.AuthContextRef),
		Format:            strings.TrimSpace(string(input.Format)),
		Scope:             strings.TrimSpace(string(input.Scope)),
		Status:            strings.TrimSpace(string(input.Status)),
		ReportRunID:       strings.TrimSpace(input.ReportRunID),
		ReportRunRevision: input.ReportRunRevision,
		ExportRequestID:   strings.TrimSpace(input.ExportRequestID),
		ReportSpec:        cloneJSON(input.ReportSpec),
		ReportFill:        cloneJSON(input.ReportFill),
		ReportPrint:       cloneJSON(input.ReportPrint),
		Metadata:          cloneJSON(metadata),
		ArtifactID:        strings.TrimSpace(input.ArtifactID),
		Error:             strings.TrimSpace(input.Error),
		Diagnostics:       diagnostics,
		SubmittedAt:       input.SubmittedAt,
		StartedAt:         cloneTime(input.StartedAt),
		CompletedAt:       cloneTime(input.CompletedAt),
		RetentionTTL:      input.RetentionTTL,
	}
}

func decodeJob(input *reportjobmodel.Record) (*ExportJob, error) {
	if input == nil {
		return nil, nil
	}
	var diagnostics []Diagnostic
	if len(input.Diagnostics) > 0 {
		if err := json.Unmarshal(input.Diagnostics, &diagnostics); err != nil {
			return nil, err
		}
	}
	metadata, resourcePin, err := decodeExportJobMetadata(input.Metadata)
	if err != nil {
		return nil, err
	}
	dependencyPins, dependencyToken := decodeExportDependencies(input.Metadata)
	return &ExportJob{
		DependencyPins: dependencyPins, DependencyToken: dependencyToken,
		JobID:             strings.TrimSpace(input.JobID),
		ArtifactRef:       strings.TrimSpace(input.ArtifactRef),
		OwnerID:           strings.TrimSpace(input.OwnerID),
		ConversationID:    strings.TrimSpace(input.ConversationID),
		WorkspaceID:       strings.TrimSpace(input.WorkspaceID),
		AuthContextRef:    strings.TrimSpace(input.AuthContextRef),
		Format:            ExportFormat(strings.TrimSpace(input.Format)),
		Scope:             ExportScope(strings.TrimSpace(input.Scope)),
		Status:            JobStatus(strings.TrimSpace(input.Status)),
		ReportRunID:       strings.TrimSpace(input.ReportRunID),
		ReportRunRevision: input.ReportRunRevision,
		ExportRequestID:   strings.TrimSpace(input.ExportRequestID),
		ReportSpec:        cloneJSON(input.ReportSpec),
		ReportFill:        cloneJSON(input.ReportFill),
		ReportPrint:       cloneJSON(input.ReportPrint),
		Metadata:          cloneJSON(metadata),
		ResourcePin:       cloneResolvedResource(resourcePin),
		ArtifactID:        strings.TrimSpace(input.ArtifactID),
		Error:             strings.TrimSpace(input.Error),
		Diagnostics:       cloneDiagnostics(diagnostics),
		SubmittedAt:       input.SubmittedAt,
		StartedAt:         cloneTime(input.StartedAt),
		CompletedAt:       cloneTime(input.CompletedAt),
		RetentionTTL:      input.RetentionTTL,
	}, nil
}

func encodeArtifact(input *Artifact) *reportartifactmodel.Record {
	if input == nil {
		return nil
	}
	return &reportartifactmodel.Record{
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       strings.TrimSpace(string(input.Format)),
		ContentType:  strings.TrimSpace(input.ContentType),
		Data:         append([]byte{}, input.Data...),
		CreatedAt:    input.CreatedAt,
		RetentionTTL: input.RetentionTTL,
	}
}

func decodeArtifact(input *reportartifactmodel.Record) (*Artifact, error) {
	if input == nil {
		return nil, nil
	}
	return &Artifact{
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       ExportFormat(strings.TrimSpace(input.Format)),
		ContentType:  strings.TrimSpace(input.ContentType),
		SourceURL:    "",
		Data:         append([]byte{}, input.Data...),
		CreatedAt:    input.CreatedAt,
		RetentionTTL: input.RetentionTTL,
	}, nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	next := *value
	return &next
}

func encodeSharedArtifact(input *SharedArtifact) (*reportshareartifactmodel.Record, error) {
	if input == nil {
		return nil, nil
	}
	metadata, err := encodeSharedArtifactMetadata(input.Metadata, input.Resource, input.sharedActorScope, input.ResourceDefinition, input.personalSource)
	if err != nil {
		return nil, err
	}
	return &reportshareartifactmodel.Record{
		ArtifactID:       strings.TrimSpace(input.ArtifactID),
		ArtifactRef:      strings.TrimSpace(input.ArtifactRef),
		OwnerID:          strings.TrimSpace(input.OwnerID),
		OwnerRef:         strings.TrimSpace(input.OwnerRef),
		Kind:             strings.TrimSpace(input.Kind),
		Lifecycle:        strings.TrimSpace(input.Lifecycle),
		Version:          input.Version,
		ReportID:         strings.TrimSpace(input.ReportID),
		Title:            strings.TrimSpace(input.Title),
		SourceArtifactID: strings.TrimSpace(input.SourceArtifactID),
		BaseArtifactRef:  strings.TrimSpace(input.BaseArtifactRef),
		PolicyRef:        strings.TrimSpace(input.PolicyRef),
		DocumentVersion:  input.DocumentVersion,
		Document:         cloneJSON(input.Document),
		ReportSpec:       cloneJSON(input.ReportSpec),
		CompileState:     cloneJSON(input.CompileState),
		ReportFill:       cloneJSON(input.ReportFill),
		ReportPrint:      cloneJSON(input.ReportPrint),
		SavedViewOverlay: cloneJSON(input.SavedViewOverlay),
		Metadata:         cloneJSON(metadata),
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        cloneTime(input.UpdatedAt),
	}, nil
}

func decodeSharedArtifact(input *reportshareartifactmodel.Record) (*SharedArtifact, error) {
	if input == nil {
		return nil, nil
	}
	metadata, resource, actorScope, definition, personalSource, err := decodeSharedArtifactMetadata(input.Metadata)
	if err != nil {
		return nil, errors.Join(forgeresource.ErrStoredDefinition, err)
	}
	return &SharedArtifact{
		ArtifactID:         strings.TrimSpace(input.ArtifactID),
		ArtifactRef:        strings.TrimSpace(input.ArtifactRef),
		OwnerID:            strings.TrimSpace(input.OwnerID),
		OwnerRef:           strings.TrimSpace(input.OwnerRef),
		Kind:               strings.TrimSpace(input.Kind),
		Lifecycle:          strings.TrimSpace(input.Lifecycle),
		Version:            input.Version,
		ReportID:           strings.TrimSpace(input.ReportID),
		Title:              strings.TrimSpace(input.Title),
		SourceArtifactID:   strings.TrimSpace(input.SourceArtifactID),
		BaseArtifactRef:    strings.TrimSpace(input.BaseArtifactRef),
		PolicyRef:          strings.TrimSpace(input.PolicyRef),
		DocumentVersion:    input.DocumentVersion,
		Document:           cloneJSON(input.Document),
		ReportSpec:         cloneJSON(input.ReportSpec),
		CompileState:       cloneJSON(input.CompileState),
		ReportFill:         cloneJSON(input.ReportFill),
		ReportPrint:        cloneJSON(input.ReportPrint),
		SavedViewOverlay:   cloneJSON(input.SavedViewOverlay),
		Metadata:           cloneJSON(metadata),
		Resource:           resource,
		ResourceDefinition: definition,
		sharedActorScope:   actorScope,
		personalSource:     personalSource,
		CreatedAt:          input.CreatedAt,
		UpdatedAt:          cloneTime(input.UpdatedAt),
	}, nil
}

func isStoreNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, reportstore.ErrNotFound)
}

func translateStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, reportstore.ErrAlreadyExists) {
		return fmt.Errorf("%w: %w", ErrAlreadyExists, err)
	}
	if errors.Is(err, reportstore.ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, reportstore.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// UpdateSharedArtifactCAS exposes the existing store's optional atomic write.
func (s *storeAdapter) UpdateSharedArtifactCAS(ctx context.Context, artifact *SharedArtifact, expected int) error {
	writer, ok := s.client.(interface {
		UpdateSharedArtifactCAS(context.Context, *reportshareartifactmodel.Record, int) error
	})
	if !ok {
		return fmt.Errorf("reporting store does not support atomic report updates")
	}
	row, err := encodeSharedArtifact(artifact)
	if err != nil {
		return err
	}
	err = writer.UpdateSharedArtifactCAS(ctx, row, expected)
	if errors.Is(err, reportstore.ErrCASMismatch) {
		return fmt.Errorf("%w: %w", identity.ErrResourceStale, err)
	}
	return err
}
