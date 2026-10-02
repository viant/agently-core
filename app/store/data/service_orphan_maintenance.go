package data

import (
	"context"
	"github.com/viant/agently-core/internal/store/orphanmaintenance"
	"time"
)

// OrphanMaintenanceAction describes the operation allowed for a reported
// orphan. The action is part of the static schema contract and is never
// supplied by the cleanup caller.
type OrphanMaintenanceAction string

const (
	OrphanMaintenanceSafeDelete OrphanMaintenanceAction = "safe-delete"
	OrphanMaintenanceSafeDetach OrphanMaintenanceAction = "safe-detach"
	OrphanMaintenanceReportOnly OrphanMaintenanceAction = "report-only"
)

const (
	orphanMaintenanceCursorSeparator      = "\x1e"
	orphanMaintenanceCursorOrderSeparator = "\x1d"
	orphanMaintenanceRecordSeparator      = "\x1f"
)

// OrphanMaintenanceCandidateRequest defines a bounded, keyset-paged orphan
// report. OlderThan is the grace-period cutoff; AfterCursor is opaque and must
// come from a previously returned candidate.
type OrphanMaintenanceCandidateRequest struct {
	OlderThan   time.Time
	AfterCursor string
	Limit       int
}

// OrphanMaintenanceCandidate contains identifiers only. The orphan scan never
// selects message content, payload bodies, report documents, or other large
// values.
type OrphanMaintenanceCandidate struct {
	CursorID       string
	RuleID         string
	Action         OrphanMaintenanceAction
	Table          string
	RecordID       string
	ReferenceTable string
	ReferenceID    string
	ObservedAt     time.Time
}

func (s *datlyService) ListOrphanMaintenanceCandidates(ctx context.Context, request OrphanMaintenanceCandidateRequest) ([]OrphanMaintenanceCandidate, error) {
	rows, err := (&orphanmaintenance.Store{Invoker: s.native}).List(ctx, orphanmaintenance.CandidateRequest{OlderThan: request.OlderThan, AfterCursor: request.AfterCursor, Limit: request.Limit})
	if err != nil {
		return nil, mapOrphanMaintenanceError(err)
	}
	result := make([]OrphanMaintenanceCandidate, 0, len(rows))
	for _, row := range rows {
		result = append(result, OrphanMaintenanceCandidate{CursorID: row.CursorID, RuleID: row.RuleID, Action: OrphanMaintenanceAction(row.Action), Table: row.Table, RecordID: row.RecordID, ReferenceTable: row.ReferenceTable, ReferenceID: row.ReferenceID, ObservedAt: row.ObservedAt})
	}
	return result, nil
}
