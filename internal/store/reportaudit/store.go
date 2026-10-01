// Package reportaudit maps reporting audit events to the canonical Datly v1
// reader and append-only writer.
package reportaudit

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	read "github.com/viant/agently-core/internal/datly/reporting/audit/read"
	write "github.com/viant/agently-core/internal/datly/reporting/audit/write"
	dexec "github.com/viant/datly/exec"
	"github.com/viant/datly/spec"
)

type Event struct {
	ID, Type, ArtifactRef                string
	Version                              int64
	JobID, ArtifactID, ActorID, ActorRef string
	OccurredAt                           time.Time
	MetadataJSON                         []byte
}

type Store struct{ Invoker dexec.ComponentInvoker }

var readerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[read.ReaderComponent]().PkgPath(), Name: "reader"},
	Route:     spec.RouteRef{Method: "GET", Path: "/v1/internal/forge/reporting/audit"},
}
var writerTarget = dexec.ComponentTarget{
	Component: spec.Key{Kind: spec.KindComponent, Scope: reflect.TypeFor[write.WriterComponent]().PkgPath(), Name: "writer"},
	Route:     spec.RouteRef{Method: "PATCH", Path: "/v1/internal/forge/reporting/audit"},
}

func (s *Store) Exists(ctx context.Context, eventID string) (bool, error) {
	if s == nil || s.Invoker == nil {
		return false, fmt.Errorf("report audit component invoker is required")
	}
	if strings.TrimSpace(eventID) == "" {
		return false, fmt.Errorf("report audit event id is required")
	}
	input := &read.Input{}
	input.SetEventID(strings.TrimSpace(eventID))
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: readerTarget, Input: input})
	if err != nil {
		return false, err
	}
	output, ok := value.(*read.Output)
	if !ok || output == nil {
		return false, fmt.Errorf("report audit reader returned %T", value)
	}
	return len(output.Data) > 0, nil
}

// Append lets the generated writer allocate an event ID and timestamp when
// omitted. The caller supplies trusted reportauditaccess through the host.
func (s *Store) Append(ctx context.Context, event Event) error {
	if s == nil || s.Invoker == nil {
		return fmt.Errorf("report audit component invoker is required")
	}
	row := &write.AuditEvent{}
	if id := strings.TrimSpace(event.ID); id != "" {
		row.SetEventId(id)
	}
	row.SetEventType(strings.TrimSpace(event.Type))
	row.SetArtifactRef(strings.TrimSpace(event.ArtifactRef))
	row.SetVersion(event.Version)
	row.SetActorId(strings.TrimSpace(event.ActorID))
	if job := strings.TrimSpace(event.JobID); job != "" {
		row.SetJobId(&job)
	}
	if artifact := strings.TrimSpace(event.ArtifactID); artifact != "" {
		row.SetArtifactId(&artifact)
	}
	if actor := strings.TrimSpace(event.ActorRef); actor != "" {
		row.SetActorRef(&actor)
	}
	if !event.OccurredAt.IsZero() {
		at := event.OccurredAt.UTC()
		row.SetOccurredAt(&at)
	}
	if event.MetadataJSON != nil {
		row.SetMetadataJson(append([]byte(nil), event.MetadataJSON...))
	}
	input := &write.Input{}
	input.SetEvents([]*write.AuditEvent{row})
	value, err := s.Invoker.InvokeComponent(ctx, dexec.ComponentRequest{Target: writerTarget, Input: input})
	if err != nil {
		return err
	}
	if _, ok := value.(*write.Output); !ok {
		return fmt.Errorf("report audit writer returned %T", value)
	}
	return nil
}

// ImportIfAbsent preserves the filesystem importer policy: an event with the
// deterministic ID is skipped. A concurrent winner is re-read after a failed
// append so the same policy holds when importers race.
func (s *Store) ImportIfAbsent(ctx context.Context, event Event) error {
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("report audit import id is required")
	}
	exists, err := s.Exists(ctx, event.ID)
	if err != nil || exists {
		return err
	}
	if err = s.Append(ctx, event); err == nil {
		return nil
	}
	exists, readErr := s.Exists(ctx, event.ID)
	if readErr == nil && exists {
		return nil
	}
	return err
}
