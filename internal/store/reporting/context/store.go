package contextstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/context/read"
	write "github.com/viant/agently-core/internal/datly/reporting/context/write"
	"github.com/viant/bindly/locator"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/runtime/handler/provider"
	"github.com/viant/datly/spec"
	"github.com/viant/sqlx/io/errx"
	xhandler "github.com/viant/xdatly/handler"
)

var (
	ErrNotFound    = errors.New("reporting store: not found")
	ErrCASMismatch = errors.New("reporting store: revision mismatch")
)

type Record struct {
	OwnerID           string
	ConversationID    string
	ActiveReportRunID string
	Revision          int64
	ActivationSource  string
	ActorID           string
	UpdatedAt         time.Time
}

// Store preserves the existing report-context API using the one generated
// reader and writer for conversation_report_context. OwnerID must resolve from
// trusted application context; no database handle is held here.
type Store struct {
	Invoker dexec.ComponentInvoker
	OwnerID func(context.Context) string
}

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/conversation-context"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/conversation-context"},
}

func (s *Store) owner(ctx context.Context) (string, error) {
	if s == nil || s.Invoker == nil || s.OwnerID == nil {
		return "", fmt.Errorf("report context store is not configured")
	}
	return strings.TrimSpace(s.OwnerID(ctx)), nil
}

func accessProviders(owner string) []locator.Provider {
	return []locator.Provider{
		provider.Named("reportaccess", func(context.Context, reflect.Type, string) (any, bool, error) { return false, true, nil }),
		provider.Named("visibility", func(context.Context, reflect.Type, string) (any, bool, error) { return &owner, true, nil }),
	}
}

func (s *Store) Get(ctx context.Context, conversationID string) (*Record, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	conversationID = strings.TrimSpace(conversationID)
	if owner == "" || conversationID == "" {
		return nil, ErrNotFound
	}
	input := &read.Input{}
	input.SetConversationID(conversationID)
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input, Providers: accessProviders(owner)})
	if err != nil {
		return nil, err
	}
	out, ok := value.(*read.Output)
	if !ok || out == nil {
		return nil, fmt.Errorf("report context reader returned %T", value)
	}
	if len(out.Data) == 0 {
		return nil, ErrNotFound
	}
	if len(out.Data) != 1 || out.Data[0] == nil {
		return nil, fmt.Errorf("report context lookup returned %d rows", len(out.Data))
	}
	row := out.Data[0]
	return &Record{OwnerID: row.OwnerId, ConversationID: row.ConversationId, ActiveReportRunID: row.ActiveReportRunId,
		Revision: row.Revision, ActivationSource: row.ActivationSource, ActorID: row.ActorId, UpdatedAt: row.UpdatedAt}, nil
}

// PutCAS inserts when expectedRevision is zero, or atomically compares the
// persisted revision on update. The business hook advances only the working
// revision after Datly captures the supplied expected token.
func (s *Store) PutCAS(ctx context.Context, record *Record, expectedRevision int64) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	if record == nil || owner == "" || owner != strings.TrimSpace(record.OwnerID) || strings.TrimSpace(record.ConversationID) == "" {
		return ErrNotFound
	}
	entity := &write.Context{}
	entity.SetOwnerId(owner)
	entity.SetConversationId(strings.TrimSpace(record.ConversationID))
	entity.SetActiveReportRunId(strings.TrimSpace(record.ActiveReportRunID))
	entity.SetRevision(expectedRevision)
	entity.SetActivationSource(strings.TrimSpace(record.ActivationSource))
	entity.SetActorId(strings.TrimSpace(record.ActorID))
	entity.SetUpdatedAt(record.UpdatedAt.UTC())
	input := &write.Input{}
	input.SetDesiredRevision(record.Revision)
	input.SetContexts([]*write.Context{entity})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input, Providers: accessProviders(owner)})
	if err != nil {
		var conflict *xhandler.Conflict
		switch {
		case errors.Is(err, write.ErrOwnerDenied), errors.Is(err, write.ErrNotFound):
			return ErrNotFound
		case errors.Is(err, write.ErrCASMismatch), errors.As(err, &conflict), errx.IsDuplicateKey(err):
			return ErrCASMismatch
		default:
			return err
		}
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("report context writer returned %T", value)
	}
	return nil
}
