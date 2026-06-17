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
	return s.client.CreateJob(ctx, encodeJob(job))
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

func (s *storeAdapter) UpdateJob(ctx context.Context, job *ExportJob) error {
	return s.client.UpdateJob(ctx, encodeJob(job))
}

func (s *storeAdapter) PutArtifact(ctx context.Context, artifact *Artifact) error {
	return s.client.PutArtifact(ctx, encodeArtifact(artifact))
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

func encodeJob(input *ExportJob) *reportjob.Record {
	if input == nil {
		return nil
	}
	diagnostics, _ := json.Marshal(input.Diagnostics)
	return &reportjob.Record{
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       strings.TrimSpace(string(input.Format)),
		Scope:        strings.TrimSpace(string(input.Scope)),
		Status:       strings.TrimSpace(string(input.Status)),
		ReportSpec:   cloneJSON(input.ReportSpec),
		ReportFill:   cloneJSON(input.ReportFill),
		ReportPrint:  cloneJSON(input.ReportPrint),
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		Error:        strings.TrimSpace(input.Error),
		Diagnostics:  diagnostics,
		SubmittedAt:  input.SubmittedAt,
		StartedAt:    cloneTime(input.StartedAt),
		CompletedAt:  cloneTime(input.CompletedAt),
		RetentionTTL: input.RetentionTTL,
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
		JobID:        strings.TrimSpace(input.JobID),
		ArtifactRef:  strings.TrimSpace(input.ArtifactRef),
		OwnerID:      strings.TrimSpace(input.OwnerID),
		Format:       ExportFormat(strings.TrimSpace(input.Format)),
		Scope:        ExportScope(strings.TrimSpace(input.Scope)),
		Status:       JobStatus(strings.TrimSpace(input.Status)),
		ReportSpec:   cloneJSON(input.ReportSpec),
		ReportFill:   cloneJSON(input.ReportFill),
		ReportPrint:  cloneJSON(input.ReportPrint),
		ArtifactID:   strings.TrimSpace(input.ArtifactID),
		Error:        strings.TrimSpace(input.Error),
		Diagnostics:  cloneDiagnostics(diagnostics),
		SubmittedAt:  input.SubmittedAt,
		StartedAt:    cloneTime(input.StartedAt),
		CompletedAt:  cloneTime(input.CompletedAt),
		RetentionTTL: input.RetentionTTL,
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

func isStoreNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || strings.Contains(strings.ToLower(err.Error()), "not found")
}
