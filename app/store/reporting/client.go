package reporting

import (
	"context"

	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
)

// Client persists reporting export jobs and artifacts.
type Client interface {
	CreateJob(ctx context.Context, job *reportjob.Record) error
	GetJob(ctx context.Context, jobID string) (*reportjob.Record, error)
	UpdateJob(ctx context.Context, job *reportjob.Record) error
	PutArtifact(ctx context.Context, artifact *reportartifact.Record) error
	GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error)
}
