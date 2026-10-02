package exportsubmit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/viant/agently-core/internal/datly/queryselectors"
	"reflect"
	"strings"
	"time"

	jobread "github.com/viant/agently-core/internal/datly/reporting/job/read"
	jobwrite "github.com/viant/agently-core/internal/datly/reporting/job/write"
	runread "github.com/viant/agently-core/internal/datly/reporting/run/read"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	rh "github.com/viant/datly/runtime/handler"
	custom "github.com/viant/datly/runtime/handler/custom"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xdatly "github.com/viant/xdatly"
	"github.com/viant/xdatly/handler"
)

var (
	ErrNotFound          = errors.New("reporting store: not found")
	ErrConflict          = errors.New("reporting store: conflict")
	ErrInvalidTransition = errors.New("reporting store: invalid job transition")
	ErrAlreadyExists     = errors.New("reporting store: already exists")
)

type Component struct {
	Contract xdatly.Component[Input, Output] `component:"RunExportSubmit,path=/v1/internal/forge/reporting/export/submit,method=POST,handler=NewSubmit,internal=true"`
}

// Input retains the caller fields that the legacy candidate validator checks.
// Snapshot bytes and the run revision are deliberately read from the run.
type Input struct {
	JobID             string        `parameter:"JobID,kind=body,in=jobId,required=true"`
	OwnerID           string        `parameter:"OwnerID,kind=body,in=ownerId,required=true"`
	ConversationID    string        `parameter:"ConversationID,kind=body,in=conversationId,required=true"`
	ReportRunID       string        `parameter:"ReportRunID,kind=body,in=reportRunId,required=true"`
	ExportRequestID   string        `parameter:"ExportRequestID,kind=body,in=exportRequestId,required=true"`
	ArtifactRef       string        `parameter:"ArtifactRef,kind=body,in=artifactRef,required=true"`
	Format            string        `parameter:"Format,kind=body,in=format,required=true"`
	Scope             string        `parameter:"Scope,kind=body,in=scope,required=true"`
	Status            string        `parameter:"Status,kind=body,in=status,required=true"`
	AuthContextRef    string        `parameter:"AuthContextRef,kind=body,in=authContextRef"`
	SubmittedAt       time.Time     `parameter:"SubmittedAt,kind=body,in=submittedAt,required=true"`
	ReportRunRevision int64         `parameter:"ReportRunRevision,kind=body,in=reportRunRevision"`
	WorkspaceID       string        `parameter:"WorkspaceID,kind=body,in=workspaceId"`
	Metadata          []byte        `parameter:"Metadata,kind=body,in=metadata"`
	ArtifactID        string        `parameter:"ArtifactID,kind=body,in=artifactId"`
	ErrorText         string        `parameter:"ErrorText,kind=body,in=error"`
	Diagnostics       []byte        `parameter:"Diagnostics,kind=body,in=diagnostics"`
	StartedAt         *time.Time    `parameter:"StartedAt,kind=body,in=startedAt"`
	CompletedAt       *time.Time    `parameter:"CompletedAt,kind=body,in=completedAt"`
	RetentionTTL      time.Duration `parameter:"RetentionTTL,kind=body,in=retentionTtl"`
}

type Output struct {
	Job    *jobread.Job `json:"job"`
	Replay bool         `json:"replay"`
}

type Submit struct{}

func NewSubmit() handler.Contract[Input, Output] { return &Submit{} }

var _ handler.Contract[Input, Output] = (*Submit)(nil)

// DatlyHandler binds the holder's declared handler to its typed implementation.
// Linked package discovery calls this provider without a host registration list.
func (Component) DatlyHandler(name string) func() (rh.TypedHandler, error) {
	if name != "NewSubmit" {
		return nil
	}
	return custom.Factory(NewSubmit)
}

func (*Submit) Exec(ctx context.Context, session handler.Session, input *Input, output *Output) error {
	if session == nil || session.Binder() == nil || input == nil || output == nil {
		return fmt.Errorf("run export submit invocation is incomplete")
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
		return fmt.Errorf("run export submit capabilities are unavailable")
	}
	owner := strings.TrimSpace(*deps.Owner)
	if owner == "" || owner != strings.TrimSpace(input.OwnerID) {
		return ErrNotFound
	}
	if err := validateCandidate(input); err != nil {
		return err
	}
	if err := deps.Starter.Start(ctx); err != nil {
		return err
	}
	// Serialize one source run before checking the export identity. Both
	// queries use current locking reads so an outer repeatable-read snapshot
	// cannot hide a job committed while this invocation waited for the run.
	runLookup := &runread.Input{}
	runLookup.SetReportRunID(strings.TrimSpace(input.ReportRunID))
	value, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: runReaderTarget, Input: runLookup, Providers: lockedReportingReads()})
	if err != nil {
		return err
	}
	runs, ok := value.(*runread.Output)
	if !ok || runs == nil {
		return fmt.Errorf("source run reader returned %T", value)
	}
	lookup := &jobread.Input{}
	lookup.SetConversationID(strings.TrimSpace(input.ConversationID))
	lookup.SetExportRequestID(strings.TrimSpace(input.ExportRequestID))
	existing, err := readJobs(ctx, deps.Invoker, lookup)
	if err != nil {
		return err
	}
	if len(existing) > 1 {
		return fmt.Errorf("export request returned multiple jobs")
	}
	if len(existing) == 1 {
		if sameRequest(existing[0], input) {
			output.Job, output.Replay = existing[0], true
			return nil
		}
		return ErrConflict
	}
	if len(runs.Data) == 0 {
		return ErrNotFound
	}
	if len(runs.Data) != 1 || runs.Data[0] == nil {
		return fmt.Errorf("source run lookup returned %d rows", len(runs.Data))
	}
	run := runs.Data[0]
	if run.OwnerId != owner || strings.TrimSpace(run.ConversationId) == "" || strings.TrimSpace(run.ConversationId) != strings.TrimSpace(input.ConversationID) {
		return ErrNotFound
	}
	if run.Status != "completed" || run.Revision < 1 || len(bytes.TrimSpace(run.ReportSpecJson)) == 0 ||
		len(bytes.TrimSpace(run.ReportFillJson)) == 0 || len(bytes.TrimSpace(run.ReportPrintJson)) == 0 {
		return ErrInvalidTransition
	}
	job := &jobwrite.Job{}
	job.SetJobId(strings.TrimSpace(input.JobID))
	job.SetArtifactRef(strings.TrimSpace(input.ArtifactRef))
	job.SetOwnerId(owner)
	conversation, runID, requestID := run.ConversationId, run.ReportRunId, strings.TrimSpace(input.ExportRequestID)
	job.SetConversationId(&conversation)
	if authContext := strings.TrimSpace(input.AuthContextRef); authContext != "" {
		job.SetAuthContextRef(&authContext)
	}
	job.SetFormat("pdf")
	job.SetScope("draft")
	job.SetStatus("queued")
	job.SetReportRunId(&runID)
	revision := run.Revision
	job.SetReportRunRevision(&revision)
	job.SetExportRequestId(&requestID)
	job.SetReportSpecJson(append([]byte(nil), run.ReportSpecJson...))
	job.SetReportFillJson(append([]byte(nil), run.ReportFillJson...))
	job.SetReportPrintJson(append([]byte(nil), run.ReportPrintJson...))
	job.SetSubmittedAt(input.SubmittedAt.UTC())
	job.SetRetentionTtlSec(0)
	mutation := &jobwrite.Input{}
	mutation.SetMode("submit")
	mutation.SetJobs([]*jobwrite.Job{job})
	if _, err := deps.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: jobWriterTarget, Input: mutation}); err != nil {
		if errx.IsDuplicateKey(err) {
			return ErrAlreadyExists
		}
		return err
	}
	byID := &jobread.Input{}
	byID.SetJobID(job.JobId)
	rows, err := readJobs(ctx, deps.Invoker, byID)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] == nil {
		return fmt.Errorf("submitted job is not readable")
	}
	output.Job = rows[0]
	return nil
}

func validateCandidate(input *Input) error {
	runID, requestID := strings.TrimSpace(input.ReportRunID), strings.TrimSpace(input.ExportRequestID)
	if strings.TrimSpace(input.JobID) == "" || runID == "" || strings.TrimSpace(input.ConversationID) == "" ||
		requestID == "" || len(requestID) > 128 || strings.TrimSpace(input.ArtifactRef) != "report-run://"+runID ||
		strings.TrimSpace(input.Format) != "pdf" || strings.TrimSpace(input.Scope) != "draft" ||
		strings.TrimSpace(input.Status) != "queued" || input.ReportRunRevision != 0 ||
		strings.TrimSpace(input.WorkspaceID) != "" || len(input.Metadata) != 0 ||
		strings.TrimSpace(input.ArtifactID) != "" || strings.TrimSpace(input.ErrorText) != "" ||
		len(input.Diagnostics) != 0 || input.StartedAt != nil || input.CompletedAt != nil || input.RetentionTTL != 0 {
		return ErrConflict
	}
	return nil
}

func sameRequest(existing *jobread.Job, input *Input) bool {
	return existing != nil && input != nil && strings.TrimSpace(existing.ReportRunId) == strings.TrimSpace(input.ReportRunID) &&
		strings.TrimSpace(existing.Format) == strings.TrimSpace(input.Format) && strings.TrimSpace(existing.Scope) == strings.TrimSpace(input.Scope)
}

func readJobs(ctx context.Context, invoker dexec.ComponentInvoker, input *jobread.Input) ([]*jobread.Job, error) {
	value, err := invoker.InvokeComponent(ctx, dexec.ComponentRequest{ReaderOptions: queryselectors.ForUpdateOptions(ctx, true), Target: jobReaderTarget, Input: input, Providers: lockedReportingReads()})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*jobread.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("job reader returned %T", value)
	}
	return out.Data, nil
}

// The private composition requests row locks without bypassing owner predicates.
func lockedReportingReads() []locator.Provider {
	return []locator.Provider{provider.Named("reportaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
		switch name {
		case "internal":
			return false, true, nil
		}
		return nil, false, nil
	})}
}

var jobReaderTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/job"}}
var runReaderTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[runread.ReaderComponent]().PkgPath(), Name: "reader"}, Route: spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/run"}}
var jobWriterTarget = dexec.ComponentTarget{Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[jobwrite.WriterComponent]().PkgPath(), Name: "writer"}, Route: spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/job"}}
