package exportcomplete

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	artifactread "github.com/viant/agently-core/internal/datly/reporting/artifact/read"
	artifactwrite "github.com/viant/agently-core/internal/datly/reporting/artifact/write"
	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/x"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

var (
	ErrNotFound          = errors.New("reporting store: not found")
	ErrConflict          = errors.New("reporting store: conflict")
	ErrInvalidTransition = errors.New("reporting store: invalid job transition")
)

type Component struct {
	Contract xdatly.Component[Input, Output] `component:"ExportComplete,path=/v1/internal/forge/reporting/export/complete,method=POST,handler=NewComplete,internal=true"`
}

// Input contains candidate artifact bytes and completion metadata. The job
// owns its reference, format and owner; callers cannot replace those fields.
type Input struct {
	JobID             string        `parameter:"JobID,kind=body,in=jobId,required=true"`
	ArtifactID        string        `parameter:"ArtifactID,kind=body,in=artifactId,required=true"`
	ContentType       string        `parameter:"ContentType,kind=body,in=contentType,required=true"`
	Data              []byte        `parameter:"Data,kind=body,in=data"`
	ArtifactCreatedAt time.Time     `parameter:"ArtifactCreatedAt,kind=body,in=artifactCreatedAt,required=true"`
	Diagnostics       []byte        `parameter:"Diagnostics,kind=body,in=diagnostics"`
	CompletedAt       time.Time     `parameter:"CompletedAt,kind=body,in=completedAt,required=true"`
	RetentionTTL      time.Duration `parameter:"RetentionTTL,kind=body,in=retentionTtl"`
}

type Output struct {
	Job *jobread.Job `json:"job"`
}

type Complete struct{}

func NewComplete() handler.Contract[Input, Output] { return &Complete{} }

var _ handler.Contract[Input, Output] = (*Complete)(nil)

func Exports() (*x.Registry, error) {
	registry := x.NewRegistry()
	for _, typ := range []reflect.Type{reflect.TypeFor[Input](), reflect.TypeFor[Output]()} {
		registry.Register(x.NewType(typ))
	}
	factory, err := x.NewFunction(reflect.TypeFor[Component]().PkgPath(), "NewComplete", custom.Factory(NewComplete))
	if err != nil {
		return nil, err
	}
	if err := registry.RegisterFunctions(factory); err != nil {
		return nil, err
	}
	return registry, nil
}

func (*Complete) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("export completion invocation is incomplete")
	}
	deps := struct {
		Invoker dexec.ComponentInvoker     `bind:"kind=component_invoker,required"`
		Starter handler.TransactionStarter `bind:"kind=transactionStarter,required"`
		Owner   *string                    `bind:"kind=visibility,in=subject,required"`
	}{}
	if err := session.Binder().Bind(ctx, &deps); err != nil {
		return err
	}
	if deps.Invoker == nil || deps.Starter == nil || deps.Owner == nil {
		return fmt.Errorf("export completion capabilities are unavailable")
	}
	owner, jobID := strings.TrimSpace(*deps.Owner), strings.TrimSpace(input.JobID)
	if owner == "" || jobID == "" || strings.TrimSpace(input.ArtifactID) == "" {
		return ErrNotFound
	}
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	jobQuery := &jobread.Input{}
	jobQuery.SetJobID(jobID)
	jobRows, err := invokeJobs(ctx, deps.Invoker, jobQuery)
	if err != nil {
		return err
	}
	if len(jobRows) == 0 {
		return ErrNotFound
	}
	if len(jobRows) != 1 || jobRows[0] == nil {
		return fmt.Errorf("job identity returned %d rows", len(jobRows))
	}
	job := jobRows[0]
	if job.OwnerId != owner {
		return ErrNotFound
	}
	artifactQuery := &artifactread.Input{}
	artifactQuery.SetJobID(jobID)
	artifacts, err := invokeArtifacts(ctx, deps.Invoker, artifactQuery)
	if err != nil {
		return err
	}
	if len(artifacts) > 1 {
		return ErrConflict
	}
	var existing *artifactread.Artifact
	if len(artifacts) == 1 {
		existing = artifacts[0]
	}
	if job.Status == "succeeded" && existing != nil && job.ArtifactId == existing.ArtifactId && sameArtifactJob(existing, job) {
		output.Job = job
		return nil
	}
	if job.Status != "running" {
		return ErrInvalidTransition
	}
	artifactID := strings.TrimSpace(input.ArtifactID)
	if existing != nil {
		if !sameArtifactJob(existing, job) {
			return ErrConflict
		}
		artifactID = existing.ArtifactId
	} else {
		artifact := &artifactwrite.Artifact{}
		artifact.SetArtifactId(artifactID)
		artifact.SetJobId(jobID)
		artifact.SetArtifactRef(job.ArtifactRef)
		artifact.SetOwnerId(owner)
		artifact.SetFormat(job.Format)
		artifact.SetContentType(strings.TrimSpace(input.ContentType))
		artifact.SetInlineData(append([]byte(nil), input.Data...))
		artifact.SetCreatedAt(input.ArtifactCreatedAt.UTC())
		artifact.SetRetentionTtlSec(ttlSeconds(input.RetentionTTL))
		artifactMutation := &artifactwrite.Input{}
		artifactMutation.SetMode("create")
		artifactMutation.SetArtifacts([]*artifactwrite.Artifact{artifact})
		if _, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: artifactWriterTarget, Input: artifactMutation}); err != nil {
			return err
		}
	}
	completed := input.CompletedAt.UTC()
	jobPatch := &jobwrite.Job{}
	jobPatch.SetJobId(jobID)
	jobPatch.SetStatus("succeeded")
	jobPatch.SetArtifactId(&artifactID)
	jobPatch.SetErrorText(nil)
	jobPatch.SetDiagnosticsJson(append([]byte(nil), input.Diagnostics...))
	jobPatch.SetCompletedAt(&completed)
	jobPatch.SetRetentionTtlSec(ttlSeconds(input.RetentionTTL))
	jobMutation := &jobwrite.Input{}
	jobMutation.SetMode("complete")
	jobMutation.SetExpectedStatus("running")
	jobMutation.SetJobs([]*jobwrite.Job{jobPatch})
	if _, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: jobWriterTarget, Input: jobMutation}); err != nil {
		return err
	}
	completedRows, err := invokeJobs(ctx, deps.Invoker, jobQuery)
	if err != nil {
		return err
	}
	if len(completedRows) != 1 || completedRows[0] == nil || completedRows[0].Status != "succeeded" {
		return fmt.Errorf("completed export job is not readable")
	}
	output.Job = completedRows[0]
	return nil
}

func ttlSeconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Second)
}

func sameArtifactJob(artifact *artifactread.Artifact, job *jobread.Job) bool {
	return artifact != nil && job != nil && strings.TrimSpace(artifact.JobId) == strings.TrimSpace(job.JobId) &&
		strings.TrimSpace(artifact.OwnerId) == strings.TrimSpace(job.OwnerId) &&
		strings.TrimSpace(artifact.ArtifactRef) == strings.TrimSpace(job.ArtifactRef) &&
		strings.EqualFold(strings.TrimSpace(artifact.Format), strings.TrimSpace(job.Format))
}

func invokeJobs(ctx context.Context, invoker dexec.ComponentInvoker, input *jobread.Input) ([]*jobread.Job, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: jobReaderTarget, Input: input, Providers: lockedReportingReads()})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*jobread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("job reader returned %T", value)
	}
	return out.Data, nil
}

func invokeArtifacts(ctx context.Context, invoker dexec.ComponentInvoker, input *artifactread.Input) ([]*artifactread.Artifact, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: artifactReaderTarget, Input: input, Providers: lockedReportingReads()})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*artifactread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("artifact reader returned %T", value)
	}
	return out.Data, nil
}

// Current locking reads serialize completion without widening owner visibility.
func lockedReportingReads() []locator.Provider {
	return []locator.Provider{provider.Named("reportaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return false, true, nil
		case "lock":
			return true, true, nil
		}
		return nil, false, nil
	})}
}

var jobReaderTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"}}
var artifactReaderTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/artifact"}}
var jobWriterTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"}}
var artifactWriterTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[artifactwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/artifact"}}
