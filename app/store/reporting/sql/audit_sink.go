package sql

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/viant/agently-core/app/store/native"
	reportaudit "github.com/viant/agently-core/internal/store/reportaudit"
	reportingsvc "github.com/viant/agently-core/service/reporting"
)

func NewAuditSink(store *Store) reportingsvc.AuditSink {
	if store == nil {
		return nil
	}
	return &auditSink{store: store}
}

type auditSink struct {
	store *Store
}

func (s *auditSink) Record(ctx context.Context, event *reportingsvc.AuditEvent) error {
	if s == nil || s.store == nil || s.store.native == nil {
		return fmt.Errorf("reporting native audit sink: store is required")
	}
	if event == nil {
		return fmt.Errorf("reporting native audit sink: event is required")
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	return (&reportaudit.Store{Invoker: s.store.native}).Append(
		native.WithAccess(ctx, native.Access{Internal: true}),
		reportaudit.Event{
			Type: event.EventType, ArtifactRef: event.ArtifactRef,
			Version: int64(event.Version), JobID: event.JobID, ArtifactID: event.ArtifactID,
			ActorID: event.ActorID, ActorRef: event.ActorRef, OccurredAt: event.OccurredAt,
			MetadataJSON: metadata,
		},
	)
}
