package sql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/viant/agently-core/app/store/native"
	reportstore "github.com/viant/agently-core/app/store/reporting"
	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	reportartifact "github.com/viant/agently-core/pkg/agently/reportartifact"
	reportjob "github.com/viant/agently-core/pkg/agently/reportjob"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

var artifactReaderTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactread.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/artifact"},
}
var artifactWriterTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactwrite.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/artifact"},
}

func (s *Store) readArtifactsNative(ctx context.Context, input *artifactread.Input) ([]*artifactread.Artifact, error) {
	if s == nil || s.native == nil {
		return nil, fmt.Errorf("native reporting runtime is required")
	}
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: hasInternalAccess(ctx)}), dexec.ComponentRequest{Target: artifactReaderTarget, Input: input})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*artifactread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report artifact reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) getArtifactNative(ctx context.Context, artifactID string) (*reportartifact.Record, error) {
	if strings.TrimSpace(artifactID) == "" || effectiveOwnerID(ctx) == "" && !hasInternalAccess(ctx) {
		return nil, errNotFound
	}
	input := &artifactread.Input{}
	input.SetArtifactID(strings.TrimSpace(artifactID))
	rows, err := s.readArtifactsNative(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("report artifact lookup returned %d rows", len(rows))
	}
	return artifactFromNative(rows[0]), nil
}

func (s *Store) listArtifactsNative(ctx context.Context) ([]*reportartifact.Record, error) {
	if effectiveOwnerID(ctx) == "" && !hasInternalAccess(ctx) {
		return []*reportartifact.Record{}, nil
	}
	rows, err := s.readArtifactsNative(ctx, &artifactread.Input{})
	if err != nil {
		return nil, err
	}
	result := make([]*reportartifact.Record, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			result = append(result, artifactFromNative(row))
		}
	}
	return result, nil
}

func (s *Store) putArtifactNative(ctx context.Context, artifact *reportartifact.Record) error {
	if artifact == nil {
		return errNotFound
	}
	if owner := effectiveOwnerID(ctx); !hasInternalAccess(ctx) && (owner == "" || owner != strings.TrimSpace(artifact.OwnerID)) {
		return errNotFound
	}
	job, err := s.getJobNative(ctx, artifact.JobID)
	if err != nil {
		return err
	}
	if !sameArtifactJob(artifact, job) {
		return reportstore.ErrConflict
	}
	query := &artifactread.Input{}
	query.SetJobID(strings.TrimSpace(artifact.JobID))
	prior, err := s.readArtifactsNative(ctx, query)
	if err != nil {
		return err
	}
	if len(prior) > 0 {
		return reportstore.ErrAlreadyExists
	}
	row := &artifactwrite.Artifact{}
	row.SetArtifactId(strings.TrimSpace(artifact.ArtifactID))
	row.SetJobId(strings.TrimSpace(artifact.JobID))
	row.SetArtifactRef(strings.TrimSpace(artifact.ArtifactRef))
	row.SetOwnerId(strings.TrimSpace(artifact.OwnerID))
	row.SetFormat(strings.TrimSpace(artifact.Format))
	row.SetContentType(strings.TrimSpace(artifact.ContentType))
	row.SetInlineData(append([]byte(nil), artifact.Data...))
	row.SetCreatedAt(artifact.CreatedAt.UTC())
	row.SetRetentionTtlSec(int64(artifact.RetentionTTL / time.Second))
	input := &artifactwrite.Input{}
	input.SetMode("create")
	input.SetArtifacts([]*artifactwrite.Artifact{row})
	value, err := s.native.InvokeComponent(native.WithAccess(ctx, native.Access{Internal: hasInternalAccess(ctx)}), dexec.ComponentRequest{Target: artifactWriterTarget, Input: input})
	if err != nil {
		switch {
		case errors.Is(err, artifactwrite.ErrNotFound), errors.Is(err, artifactwrite.ErrOwnerDenied):
			return errNotFound
		case errors.Is(err, artifactwrite.ErrConflict):
			return reportstore.ErrConflict
		case errors.Is(err, artifactwrite.ErrAlreadyExists):
			return reportstore.ErrAlreadyExists
		default:
			return err
		}
	}
	if _, ok := value.(*artifactwrite.Output); !ok {
		return fmt.Errorf("report artifact writer returned %T", value)
	}
	return nil
}

func artifactFromNative(row *artifactread.Artifact) *reportartifact.Record {
	if row == nil {
		return nil
	}
	return &reportartifact.Record{
		ArtifactID: row.ArtifactId, JobID: row.JobId, ArtifactRef: row.ArtifactRef,
		OwnerID: row.OwnerId, Format: row.Format, ContentType: row.ContentType,
		Data: append([]byte(nil), row.InlineData...), CreatedAt: row.CreatedAt,
		RetentionTTL: time.Duration(row.RetentionTtlSec) * time.Second,
	}
}

func sameArtifactJob(artifact *reportartifact.Record, job *reportjob.Record) bool {
	return artifact != nil && job != nil &&
		strings.TrimSpace(artifact.JobID) == strings.TrimSpace(job.JobID) &&
		strings.TrimSpace(artifact.OwnerID) == strings.TrimSpace(job.OwnerID) &&
		strings.TrimSpace(artifact.ArtifactRef) == strings.TrimSpace(job.ArtifactRef) &&
		strings.EqualFold(strings.TrimSpace(artifact.Format), strings.TrimSpace(job.Format))
}
