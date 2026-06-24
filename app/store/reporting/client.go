package reporting

import (
	"context"
	"errors"

	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	reportshareartifact "github.com/viant/agently-core/pkg/agently/reportshareartifact"
)

var (
	// ErrAlreadyExists indicates a storage collision on a reporting job or artifact ID.
	ErrAlreadyExists = errors.New("reporting store: already exists")
)

// Client persists reporting export jobs and artifacts.
type Client interface {
	CreateJob(ctx context.Context, job *reportjob.Record) error
	GetJob(ctx context.Context, jobID string) (*reportjob.Record, error)
	ListJobs(ctx context.Context) ([]*reportjob.Record, error)
	UpdateJob(ctx context.Context, job *reportjob.Record) error
	PutArtifact(ctx context.Context, artifact *reportartifact.Record) error
	GetArtifact(ctx context.Context, artifactID string) (*reportartifact.Record, error)
	ListArtifacts(ctx context.Context) ([]*reportartifact.Record, error)
	CreateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error
	GetSharedArtifact(ctx context.Context, artifactID string) (*reportshareartifact.Record, error)
	ListSharedArtifacts(ctx context.Context) ([]*reportshareartifact.Record, error)
	UpdateSharedArtifact(ctx context.Context, artifact *reportshareartifact.Record) error
}
