package runstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/run/read"
	write "github.com/viant/agently-core/internal/datly/reporting/run/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xhandler "github.com/viant/xdatly/handler"
)

var (
	ErrNotFound      = errors.New("reporting store: not found")
	ErrAlreadyExists = errors.New("reporting store: already exists")
	ErrCASMismatch   = errors.New("reporting store: revision mismatch")
	ErrImmutable     = errors.New("reporting store: completed run is immutable")
)

type Record struct {
	ReportRunID      string
	OwnerID          string
	ConversationID   string
	Materializer     string
	Origin           string
	BuilderRef       string
	PresetID         string
	SourceKind       string
	SourceID         string
	RequestedParams  json.RawMessage
	EffectiveParams  json.RawMessage
	Status           string
	FailureCode      string
	FailureText      string
	StartedAt        time.Time
	CompletedAt      *time.Time
	Revision         int64
	UIRunRequestID   string
	ReportSpec       json.RawMessage
	ReportFill       json.RawMessage
	ReportPrint      json.RawMessage
	ActivationSource string
	AdoptionSource   string
	ActorID          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Store maps the report-run caller to one generated reader and writer. The
// owner resolver must derive its value from trusted application context.
type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/run"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/run"},
}

func (s *Store) owner(ctx context.Context) (string, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return "", fmt.Errorf("report run store is not configured")
	}
	return strings.TrimSpace(s.OwnerID(ctx)), nil
}

func accessProviders(owner string) []locator.Provider {
	return []locator.Provider{
		provider.Named("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil }),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
}

func (s *Store) Get(ctx context.Context, id string) (*Record, error) {
	input := &read.Input{}
	input.SetReportRunID(strings.TrimSpace(id))
	return s.get(ctx, input)
}

func (s *Store) GetByRequestID(ctx context.Context, id string) (*Record, error) {
	input := &read.Input{}
	input.SetUIRunRequestID(strings.TrimSpace(id))
	return s.get(ctx, input)
}

func (s *Store) get(ctx context.Context, input *read.Input) (*Record, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	if owner == "" || input == nil || input.ReportRunID == "" && input.UIRunRequestID == "" {
		return nil, ErrNotFound
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: accessProviders(owner)})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report run reader returned %T", value)
	}
	if len(out.Data) == 0 {
		return nil, ErrNotFound
	}
	if len(out.Data) != 1 || out.Data[0] == nil {
		return nil, fmt.Errorf("report run lookup returned %d rows", len(out.Data))
	}
	return fromRead(out.Data[0]), nil
}

func (s *Store) Create(ctx context.Context, record *Record) error {
	return s.write(ctx, "create", record, 0)
}

func (s *Store) UpdateCAS(ctx context.Context, record *Record, expectedRevision int64) error {
	return s.write(ctx, "update", record, expectedRevision)
}

// AdoptCAS is the completed-manual-snapshot exception to ordinary immutability.
// The writer hook permits only the exact conversation/adoption/actor fields.
func (s *Store) AdoptCAS(ctx context.Context, record *Record, expectedRevision int64) error {
	return s.write(ctx, "adopt", record, expectedRevision)
}

func (s *Store) write(ctx context.Context, mode string, record *Record, expectedRevision int64) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if record == nil || owner == "" || owner != strings.TrimSpace(record.OwnerID) || strings.TrimSpace(record.ReportRunID) == "" {
		return ErrNotFound
	}
	entity := toWrite(record, expectedRevision, mode)
	input := &write.Input{}
	input.SetMode(mode)
	input.SetRuns([]*write.Run{entity})
	if mode == "update" || mode == "adopt" {
		input.SetDesiredRevision(record.Revision)
		input.SetExpectedRequestID(strings.TrimSpace(record.UIRunRequestID))
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: accessProviders(owner)})
	if err != nil {
		var conflict *xhandler.Conflict
		switch {
		case errors.Is(err, write.ErrOwnerDenied), errors.Is(err, write.ErrNotFound):
			return ErrNotFound
		case errors.Is(err, write.ErrImmutable):
			return ErrImmutable
		case errors.Is(err, write.ErrAlreadyExists):
			return ErrAlreadyExists
		case errors.Is(err, write.ErrCASMismatch), errors.As(err, &conflict), errx.IsDuplicateKey(err):
			if mode == "create" {
				return ErrAlreadyExists
			}
			if _, readErr := s.Get(ctx, record.ReportRunID); errors.Is(readErr, ErrNotFound) {
				return ErrNotFound
			}
			return ErrCASMismatch
		default:
			return err
		}
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("report run writer returned %T", value)
	}
	return nil
}

func toWrite(record *Record, expectedRevision int64, mode string) *write.Run {
	row := &write.Run{}
	row.SetReportRunId(strings.TrimSpace(record.ReportRunID))
	row.SetOwnerId(strings.TrimSpace(record.OwnerID))
	row.SetConversationId(nullable(record.ConversationID))
	row.SetMaterializer(strings.TrimSpace(record.Materializer))
	row.SetOrigin(nullable(record.Origin))
	row.SetBuilderRef(nullable(record.BuilderRef))
	row.SetPresetId(nullable(record.PresetID))
	row.SetSourceKind(nullable(record.SourceKind))
	row.SetSourceId(nullable(record.SourceID))
	row.SetRequestedParamsJson(copyBytes(record.RequestedParams))
	row.SetEffectiveParamsJson(copyBytes(record.EffectiveParams))
	row.SetStatus(strings.TrimSpace(record.Status))
	row.SetFailureCode(nullable(record.FailureCode))
	row.SetFailureText(nullable(record.FailureText))
	row.SetStartedAt(record.StartedAt.UTC())
	row.SetCompletedAt(copyTime(record.CompletedAt))
	if mode == "create" {
		row.SetRevision(record.Revision)
	} else {
		row.SetRevision(expectedRevision)
	}
	row.SetUiRunRequestId(strings.TrimSpace(record.UIRunRequestID))
	row.SetReportSpecJson(copyBytes(record.ReportSpec))
	row.SetReportFillJson(copyBytes(record.ReportFill))
	row.SetReportPrintJson(copyBytes(record.ReportPrint))
	row.SetActivationSource(nullable(record.ActivationSource))
	row.SetAdoptionSource(nullable(record.AdoptionSource))
	row.SetActorId(nullable(record.ActorID))
	row.SetCreatedAt(record.CreatedAt.UTC())
	row.SetUpdatedAt(record.UpdatedAt.UTC())
	return row
}

func fromRead(row *read.Run) *Record {
	return &Record{
		ReportRunID: row.ReportRunId, OwnerID: row.OwnerId, ConversationID: row.ConversationId,
		Materializer: row.Materializer, Origin: row.Origin, BuilderRef: row.BuilderRef,
		PresetID: row.PresetId, SourceKind: row.SourceKind, SourceID: row.SourceId,
		RequestedParams: copyBytes(row.RequestedParamsJson), EffectiveParams: copyBytes(row.EffectiveParamsJson),
		Status: row.Status, FailureCode: row.FailureCode, FailureText: row.FailureText,
		StartedAt: row.StartedAt, CompletedAt: copyTime(row.CompletedAt), Revision: row.Revision,
		UIRunRequestID: row.UiRunRequestId, ReportSpec: copyBytes(row.ReportSpecJson),
		ReportFill: copyBytes(row.ReportFillJson), ReportPrint: copyBytes(row.ReportPrintJson),
		ActivationSource: row.ActivationSource, AdoptionSource: row.AdoptionSource, ActorID: row.ActorId,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func nullable(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func copyBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
