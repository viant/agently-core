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
	"github.com/viant/sqlx/io/errx"
)

var (
	ErrNotFound      = errors.New("shared artifact not found")
	ErrAlreadyExists = errors.New("shared artifact already exists")
)

type errReportTooLarge struct{}

func (errReportTooLarge) Error() string {
	return "report content exceeds the MySQL storage limit of 16,777,215 bytes per field"
}
func (errReportTooLarge) HTTPStatusCode() int { return 413 }

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

func (s *Store) write(ctx context.Context, owner, intent string, record *Record, remove bool, expected ...int) error {
	// Avoid MySQL silently truncating MEDIUMBLOB values when strict mode is off.
	// Normal writes need no driver or schema lookup. SQLite has a different capacity.
	const mediumBlobLimit = (1 << 24) - 1
	if !remove && max(len(record.Metadata), len(record.Document), len(record.ReportSpec), len(record.CompileState), len(record.ReportFill), len(record.ReportPrint), len(record.SavedViewOverlay)) > mediumBlobLimit {
		driver, ok := s.Invoker.(interface {
			ConfiguredDriver(context.Context, string) (string, error)
		})
		if !ok {
			return fmt.Errorf("cannot verify the storage capacity for this report")
		}
		name, err := driver.ConfiguredDriver(ctx, "agently")
		if err != nil {
			return err
		}
		if strings.EqualFold(name, "mysql") {
			return errReportTooLarge{}
		}
	}
	entity := toWrite(record)
	if remove {
		entity.SetShouldDelete(true)
	}
	input := &write.Input{}
	input.SetArtifact(entity)
	if len(expected) > 0 {
		input.SetExpectedVersion(expected[0])
	}
	if intent != "" {
		input.SetWriteIntent(intent)
	}
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: accessProviders(owner, "")})
	if err != nil {
		switch {
		case errors.Is(err, write.ErrAlreadyExists), intent == "create" && errx.IsDuplicateKey(err):
			return fmt.Errorf("%w: %w", ErrAlreadyExists, err)
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

// ListHeaders uses the existing owner-scoped reader without fetching definition blobs.
func (s *Store) ListHeaders(ctx context.Context, offset, limit int) ([]*Record, error) {
	input := &read.Input{}
	input.SetLimit(limit)
	input.SetOffset(offset)
	input.SetFields([]string{"artifact_id", "artifact_ref", "owner_id", "kind", "lifecycle", "version", "title"})
	return s.list(ctx, input)
}

// GetMany reads exact IDs using the same owner predicate as List and Get.
func (s *Store) GetMany(ctx context.Context, ids []string) ([]*Record, error) {
	var records []*Record
	// Leave room for the reader's other bound inputs on SQLite.
	const batchSize = 500
	for len(ids) > 0 {
		n := min(len(ids), batchSize)
		input := &read.Input{}
		input.SetArtifactIDs(ids[:n])
		rows, err := s.list(ctx, input)
		if err != nil {
			return nil, err
		}
		records = append(records, rows...)
		ids = ids[n:]
	}
	return records, nil
}

func (s *Store) List(ctx context.Context) ([]*Record, error) {
	return s.list(ctx, &read.Input{})
}

func (s *Store) list(ctx context.Context, input *read.Input) ([]*Record, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	result := []*Record{}
	if owner == "" {
		return result, nil
	}
	rows, err := s.read(ctx, owner, "rows", input)
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

// UpdateCAS reuses the native mutation predicate against the existing version.
func (s *Store) UpdateCAS(ctx context.Context, record *Record, expected int) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if record == nil || owner == "" || owner != record.OwnerID {
		return ErrNotFound
	}
	if record.Version != expected+1 {
		return fmt.Errorf("shared artifact version must advance by one")
	}
	return s.write(ctx, owner, "update", record, false, expected)
}
