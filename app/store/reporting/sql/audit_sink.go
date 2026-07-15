package sql

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
	if s == nil || s.store == nil {
		return fmt.Errorf("reporting sql audit sink: store is required")
	}
	if event == nil {
		return fmt.Errorf("reporting sql audit sink: event is required")
	}
	db, err := s.store.dbHandle()
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return err
	}
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	eventID := strings.TrimSpace(event.ArtifactID) + "_" + strings.TrimSpace(event.JobID) + "_" + strings.TrimSpace(event.ActorID) + "_" + strings.TrimSpace(event.EventType)
	if strings.Trim(eventID, "_") == "" {
		eventID = uuid.NewString()
	} else {
		eventID += "_" + uuid.NewString()
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO report_audit_event (
  event_id, event_type, artifact_ref, version, job_id, artifact_id, actor_id, actor_ref, occurred_at, metadata_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		strings.TrimSpace(event.EventType),
		strings.TrimSpace(event.ArtifactRef),
		event.Version,
		nullIfEmpty(event.JobID),
		nullIfEmpty(event.ArtifactID),
		strings.TrimSpace(event.ActorID),
		nullIfEmpty(event.ActorRef),
		occurredAt.UTC(),
		metadata,
	)
	return err
}
