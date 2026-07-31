package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	reportstore "github.com/viant/agently-core/app/store/reporting"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
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
	return translateStoreError(s.client.CreateSharedArtifact(ctx, encodeSharedArtifact(artifact)))
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

func (s *storeAdapter) ListSharedArtifacts(ctx context.Context) ([]*SharedArtifact, error) {
	artifacts, err := s.client.ListSharedArtifacts(ctx)
	if err != nil {
		if isStoreNotFound(err) {
			return []*SharedArtifact{}, nil
		}
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
	return translateStoreError(s.client.UpdateSharedArtifact(ctx, encodeSharedArtifact(artifact)))
}

func (s *storeAdapter) DeleteSharedArtifact(ctx context.Context, artifactID string) error {
	return translateStoreError(s.client.DeleteSharedArtifact(ctx, strings.TrimSpace(artifactID)))
}

func encodeJob(input *ExportJob) *reportjob.Record {
	if input == nil {
		return nil
	}
	diagnostics, _ := json.Marshal(input.Diagnostics)
	return &reportjob.Record{
		JobID:          strings.TrimSpace(input.JobID),
		ArtifactRef:    strings.TrimSpace(input.ArtifactRef),
		OwnerID:        strings.TrimSpace(input.OwnerID),
		ConversationID: strings.TrimSpace(input.ConversationID),
		WorkspaceID:    strings.TrimSpace(input.WorkspaceID),
		AuthContextRef: strings.TrimSpace(input.AuthContextRef),
		Format:         strings.TrimSpace(string(input.Format)),
		Scope:          strings.TrimSpace(string(input.Scope)),
		Status:         strings.TrimSpace(string(input.Status)),
		ReportSpec:     cloneJSON(input.ReportSpec),
		ReportFill:     cloneJSON(input.ReportFill),
		ReportPrint:    cloneJSON(input.ReportPrint),
		Metadata:       cloneJSON(input.Metadata),
		ArtifactID:     strings.TrimSpace(input.ArtifactID),
		Error:          strings.TrimSpace(input.Error),
		Diagnostics:    diagnostics,
		SubmittedAt:    input.SubmittedAt,
		StartedAt:      cloneTime(input.StartedAt),
		CompletedAt:    cloneTime(input.CompletedAt),
		RetentionTTL:   input.RetentionTTL,
	}
}

func decodeJob(input *reportjob.Record) (*ExportJob, error) {
	if input == nil {
		return nil, nil
	}
	var diagnostics []Diagnostic
	if len(input.Diagnostics) > 0 {
		if err := json.Unmarshal(input.Diagnostics, &diagnostics); err != nil {
			return nil, err
		}
	}
	return &ExportJob{
		JobID:          strings.TrimSpace(input.JobID),
		ArtifactRef:    strings.TrimSpace(input.ArtifactRef),
		OwnerID:        strings.TrimSpace(input.OwnerID),
		ConversationID: strings.TrimSpace(input.ConversationID),
		WorkspaceID:    strings.TrimSpace(input.WorkspaceID),
		AuthContextRef: strings.TrimSpace(input.AuthContextRef),
		Format:         ExportFormat(strings.TrimSpace(input.Format)),
		Scope:          ExportScope(strings.TrimSpace(input.Scope)),
		Status:         JobStatus(strings.TrimSpace(input.Status)),
		ReportSpec:     cloneJSON(input.ReportSpec),
		ReportFill:     cloneJSON(input.ReportFill),
		ReportPrint:    cloneJSON(input.ReportPrint),
		Metadata:       cloneJSON(input.Metadata),
		ArtifactID:     strings.TrimSpace(input.ArtifactID),
		Error:          strings.TrimSpace(input.Error),
		Diagnostics:    cloneDiagnostics(diagnostics),
		SubmittedAt:    input.SubmittedAt,
		StartedAt:      cloneTime(input.StartedAt),
		CompletedAt:    cloneTime(input.CompletedAt),
		RetentionTTL:   input.RetentionTTL,
	}, nil
}

func encodeArtifact(input *Artifact) *reportartifact.Record {
	if input == nil {
		return nil
	}
	return &reportartifact.Record{
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

func decodeArtifact(input *reportartifact.Record) (*Artifact, error) {
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

func encodeSharedArtifact(input *SharedArtifact) *reportshareartifact.Record {
	if input == nil {
		return nil
	}
	return &reportshareartifact.Record{
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
		Metadata:         cloneJSON(input.Metadata),
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        cloneTime(input.UpdatedAt),
	}
}

func decodeSharedArtifact(input *reportshareartifact.Record) (*SharedArtifact, error) {
	if input == nil {
		return nil, nil
	}
	return &SharedArtifact{
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
		Metadata:         cloneJSON(input.Metadata),
		CreatedAt:        input.CreatedAt,
		UpdatedAt:        cloneTime(input.UpdatedAt),
	}, nil
}

func isStoreNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found")
}

func translateStoreError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, reportstore.ErrAlreadyExists) {
		return ErrAlreadyExists
	}
	return err
}
