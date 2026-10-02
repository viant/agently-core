package sharedartifact

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/read"
	write "github.com/viant/agently-core/internal/datly/reporting/sharedartifact/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	writer "github.com/viant/datly/runtime/handler/writer"
	"github.com/viant/datly/spec"
)

var (
	ErrNotFound      = errors.New("shared artifact not found")
	ErrAlreadyExists = errors.New("shared artifact already exists")
)

// Record is the application-owned shared artifact shell. JSON blobs are opaque
// to Core and copied at the adapter boundary.
type Record struct {
	ArtifactID       string
	ArtifactRef      string
	OwnerID          string
	OwnerRef         string
	Kind             string
	Lifecycle        string
	Version          int
	ReportID         string
	Title            string
	SourceArtifactID string
	BaseArtifactRef  string
	PolicyRef        string
	DocumentVersion  int
	Document         []byte
	ReportSpec       []byte
	CompileState     []byte
	ReportFill       []byte
	ReportPrint      []byte
	SavedViewOverlay []byte
	Metadata         []byte
	CreatedAt        time.Time
	UpdatedAt        *time.Time
}

// Store invokes only the one generated reader and writer for
// report_shared_artifact. OwnerID must resolve from trusted application context.
type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/shared-artifact"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/api/forge/reporting/shared-artifact"},
}

func (s *Store) owner(ctx context.Context) (string, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return "", fmt.Errorf("shared artifact store is not configured")
	}
	return strings.TrimSpace(s.OwnerID(ctx)), nil
}

func accessProviders(owner, mode string) []locator.Provider {
	return []locator.Provider{
		provider.Named("artifactaccess", func(_ context.Context, _ reflect.Type, name string) (any, bool, error) {
			if name == "mode" {
				return mode, true, nil
			}
			return false, true, nil
		}),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) {
			return &owner, true, nil
		}),
	}
}

func (s *Store) read(ctx context.Context, owner, mode string, input *read.Input) ([]*read.SharedArtifact, error) {
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: accessProviders(owner, mode)})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("shared artifact reader returned %T", value)
	}
	return out.Data, nil
}

func (s *Store) write(ctx context.Context, owner, intent string, record *Record, remove bool) error {
	entity := toWrite(record)
	if remove {
		entity.SetShouldDelete(true)
	}
	input := &write.Input{}
	input.SetArtifact(entity)
	if intent != "" {
		input.SetWriteIntent(intent)
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: accessProviders(owner, "")})
	if err != nil {
		switch {
		case errors.Is(err, write.ErrAlreadyExists):
			return ErrAlreadyExists
		case errors.Is(err, write.ErrNotFound), errors.Is(err, write.ErrOwnerDenied), errors.Is(err, writer.ErrDeleteNotFound):
			return ErrNotFound
		default:
			return err
		}
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("shared artifact writer returned %T", value)
	}
	return nil
}

func (s *Store) Create(ctx context.Context, record *Record) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if record == nil || owner == "" || owner != strings.TrimSpace(record.OwnerID) {
		return ErrNotFound
	}
	return s.write(ctx, owner, "create", record, false)
}

func (s *Store) Get(ctx context.Context, artifactID string) (*Record, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	if owner == "" || strings.TrimSpace(artifactID) == "" {
		return nil, ErrNotFound
	}
	input := &read.Input{}
	input.SetArtifactID(strings.TrimSpace(artifactID))
	rows, err := s.read(ctx, owner, "byId", input)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if len(rows) != 1 || rows[0] == nil {
		return nil, fmt.Errorf("shared artifact lookup returned %d rows", len(rows))
	}
	return fromRead(rows[0]), nil
}

func (s *Store) List(ctx context.Context) ([]*Record, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	result := []*Record{}
	if owner == "" {
		return result, nil
	}
	rows, err := s.read(ctx, owner, "rows", &read.Input{})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row != nil {
			result = append(result, fromRead(row))
		}
	}
	return result, nil
}

func (s *Store) Update(ctx context.Context, record *Record) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if record == nil || owner == "" || owner != strings.TrimSpace(record.OwnerID) {
		return ErrNotFound
	}
	return s.write(ctx, owner, "update", record, false)
}

func (s *Store) Delete(ctx context.Context, artifactID string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if owner == "" || strings.TrimSpace(artifactID) == "" {
		return ErrNotFound
	}
	return s.write(ctx, owner, "", &Record{ArtifactID: strings.TrimSpace(artifactID), OwnerID: owner}, true)
}

func toWrite(record *Record) *write.SharedArtifact {
	out := &write.SharedArtifact{}
	out.SetArtifactId(strings.TrimSpace(record.ArtifactID))
	out.SetArtifactRef(strings.TrimSpace(record.ArtifactRef))
	out.SetOwnerId(strings.TrimSpace(record.OwnerID))
	out.SetOwnerRef(strings.TrimSpace(record.OwnerRef))
	out.SetKind(strings.TrimSpace(record.Kind))
	out.SetLifecycle(strings.TrimSpace(record.Lifecycle))
	out.SetVersion(record.Version)
	out.SetReportId(strings.TrimSpace(record.ReportID))
	out.SetTitle(strings.TrimSpace(record.Title))
	out.SetSourceArtifactId(strings.TrimSpace(record.SourceArtifactID))
	out.SetBaseArtifactRef(strings.TrimSpace(record.BaseArtifactRef))
	out.SetPolicyRef(strings.TrimSpace(record.PolicyRef))
	out.SetDocumentVersion(record.DocumentVersion)
	out.SetReportDocumentJson(copyBytes(record.Document))
	out.SetReportSpecJson(copyBytes(record.ReportSpec))
	out.SetCompileStateJson(copyBytes(record.CompileState))
	out.SetReportFillJson(copyBytes(record.ReportFill))
	out.SetReportPrintJson(copyBytes(record.ReportPrint))
	out.SetSavedViewOverlayJson(copyBytes(record.SavedViewOverlay))
	out.SetMetadataJson(copyBytes(record.Metadata))
	out.SetCreatedAt(record.CreatedAt)
	out.SetUpdatedAt(copyTime(record.UpdatedAt))
	return out
}

func fromRead(row *read.SharedArtifact) *Record {
	return &Record{
		ArtifactID: row.ArtifactId, ArtifactRef: row.ArtifactRef, OwnerID: row.OwnerId,
		OwnerRef: row.OwnerRef, Kind: row.Kind, Lifecycle: row.Lifecycle, Version: row.Version,
		ReportID: row.ReportId, Title: row.Title, SourceArtifactID: row.SourceArtifactId,
		BaseArtifactRef: row.BaseArtifactRef, PolicyRef: row.PolicyRef,
		DocumentVersion: row.DocumentVersion, Document: copyBytes(row.ReportDocumentJson),
		ReportSpec: copyBytes(row.ReportSpecJson), CompileState: copyBytes(row.CompileStateJson),
		ReportFill: copyBytes(row.ReportFillJson), ReportPrint: copyBytes(row.ReportPrintJson),
		SavedViewOverlay: copyBytes(row.SavedViewOverlayJson), Metadata: copyBytes(row.MetadataJson),
		CreatedAt: row.CreatedAt, UpdatedAt: copyTime(row.UpdatedAt),
	}
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
	copy := *value
	return &copy
}
