package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	reportingsvc "github.com/viant/agently-core/service/reporting"
	"github.com/viant/agently-core/workspace"
)

// NewAuditSink constructs a filesystem-backed reporting audit sink using the
// workspace StateStore.
func NewAuditSink(stateStore workspace.StateStore) reportingsvc.AuditSink {
	return &auditSink{stateStore: stateStore}
}

type auditSink struct {
	stateStore workspace.StateStore
}

func (s *auditSink) Record(ctx context.Context, event *reportingsvc.AuditEvent) error {
	if s == nil || s.stateStore == nil {
		return fmt.Errorf("reporting fs audit sink: state store is required")
	}
	if event == nil {
		return fmt.Errorf("reporting fs audit sink: event is required")
	}
	dir, err := s.stateStore.StatePath(ctx, filepath.Join("reporting", "audits"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	filename := auditFilename(event)
	return writeJSONCreateOnly(filepath.Join(dir, filename), event,
		"reporting fs audit sink: audit %s already exists: %w", filename, os.ErrExist)
}

func auditFilename(event *reportingsvc.AuditEvent) string {
	parts := []string{
		sanitizeAuditFilenameSegment(event.EventType),
		sanitizeAuditFilenameSegment(event.ArtifactID),
		sanitizeAuditFilenameSegment(event.JobID),
		sanitizeAuditFilenameSegment(event.ActorID),
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	prefix := strings.Join(filtered, "_")
	if prefix == "" {
		prefix = "audit"
	}
	return prefix + "_" + uuid.NewString() + ".json"
}

func sanitizeAuditFilenameSegment(value string) string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"*", "-",
		"?", "-",
		"\"", "-",
		"<", "-",
		">", "-",
		"|", "-",
		" ", "_",
	)
	return replacer.Replace(normalized)
}

func readAuditEvent(path string) (*reportingsvc.AuditEvent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	event := &reportingsvc.AuditEvent{}
	if err := json.Unmarshal(data, event); err != nil {
		return nil, err
	}
	return event, nil
}
