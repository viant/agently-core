package data

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ConversationMaintenanceCandidateRequest defines a bounded, keyset-paged
// maintenance scan. It is an internal maintenance contract and does not use a
// request principal or user impersonation.
type ConversationMaintenanceCandidateRequest struct {
	Kind           ConversationMaintenanceKind
	InactiveBefore time.Time
	AfterActivity  time.Time
	AfterRootID    string
	Limit          int
}

// ConversationMaintenanceCandidate contains only metadata needed to evaluate
// a graph. In particular, the scan never reads conversation text or payloads.
type ConversationMaintenanceCandidate struct {
	RootID          string
	ExpectedOwnerID string
	ActivityAt      time.Time
}

func (s *datlyService) ListConversationMaintenanceCandidates(ctx context.Context, request ConversationMaintenanceCandidateRequest) ([]ConversationMaintenanceCandidate, error) {
	request.InactiveBefore = request.InactiveBefore.UTC()
	request.AfterActivity = request.AfterActivity.UTC()
	request.AfterRootID = strings.TrimSpace(request.AfterRootID)
	if err := validateConversationMaintenanceCandidateRequest(request); err != nil {
		return nil, err
	}

	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return nil, err
	}

	activityExpr := "COALESCE(c.last_activity, c.updated_at, c.created_at)"
	scheduledExpr := `(COALESCE(c.scheduled, 0) <> 0
 OR TRIM(COALESCE(c.schedule_id, '')) <> ''
 OR TRIM(COALESCE(c.schedule_run_id, '')) <> ''
 OR TRIM(COALESCE(c.schedule_kind, '')) <> ''
 OR EXISTS (
     SELECT 1 FROM run maintenance_run
     WHERE maintenance_run.conversation_id = c.id
       AND LOWER(TRIM(COALESCE(maintenance_run.conversation_kind, ''))) = 'scheduled'
 ))`
	kindPredicate := "NOT " + scheduledExpr
	ownerPredicate := "TRIM(COALESCE(c.created_by_user_id, '')) <> ''"
	if request.Kind == ConversationMaintenanceScheduled {
		kindPredicate = scheduledExpr
	} else if request.Kind == ConversationMaintenanceScheduledFallback {
		kindPredicate = scheduledExpr + `
  AND NOT EXISTS (
      SELECT 1 FROM run maintenance_any_run
      WHERE maintenance_any_run.conversation_id = c.id
  )`
		if capabilities.hasColumn("schedule_run", "conversation_id") {
			kindPredicate += `
  AND NOT EXISTS (
      SELECT 1 FROM schedule_run maintenance_legacy_run
      WHERE maintenance_legacy_run.conversation_id = c.id
         OR maintenance_legacy_run.id = TRIM(COALESCE(c.schedule_run_id, ''))
  )`
		}
		// System retention does not use historical owner metadata as an
		// authorization boundary. This also lets it clean legacy ownerless shells.
		ownerPredicate = "1 = 1"
	}

	args := []interface{}{request.InactiveBefore}
	var cursorPredicate string
	if request.AfterRootID != "" {
		cursorPredicate = fmt.Sprintf("\n  AND (%s > ? OR (%s = ? AND c.id > ?))", activityExpr, activityExpr)
		args = append(args, request.AfterActivity, request.AfterActivity, request.AfterRootID)
	}
	args = append(args, request.Limit)
	query := fmt.Sprintf(`SELECT c.id, TRIM(COALESCE(c.created_by_user_id, '')), CAST(%s AS CHAR)
FROM conversation c
WHERE TRIM(COALESCE(c.conversation_parent_id, '')) = ''
  AND TRIM(COALESCE(c.conversation_parent_turn_id, '')) = ''
  AND %s
  AND %s IS NOT NULL
  AND %s <= ?
  AND %s
  AND NOT EXISTS (
      SELECT 1 FROM message maintenance_link
      WHERE maintenance_link.linked_conversation_id = c.id
  )%s
ORDER BY %s ASC, c.id ASC
LIMIT ?`, activityExpr, ownerPredicate, activityExpr, activityExpr, kindPredicate, cursorPredicate, activityExpr)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ConversationMaintenanceCandidate, 0, request.Limit)
	for rows.Next() {
		var candidate ConversationMaintenanceCandidate
		var rawActivity sql.NullString
		if err = rows.Scan(&candidate.RootID, &candidate.ExpectedOwnerID, &rawActivity); err != nil {
			return nil, err
		}
		candidate.RootID = strings.TrimSpace(candidate.RootID)
		candidate.ExpectedOwnerID = strings.TrimSpace(candidate.ExpectedOwnerID)
		activityAt, ok := parseDBTime(rawActivity.String)
		if !rawActivity.Valid || !ok {
			return nil, fmt.Errorf("maintenance candidate %q has invalid activity time %q", candidate.RootID, rawActivity.String)
		}
		candidate.ActivityAt = activityAt
		result = append(result, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func validateConversationMaintenanceCandidateRequest(request ConversationMaintenanceCandidateRequest) error {
	switch {
	case request.Kind != ConversationMaintenanceInteractive && request.Kind != ConversationMaintenanceScheduled && request.Kind != ConversationMaintenanceScheduledFallback:
		return fmt.Errorf("%w: unsupported candidate kind %q", ErrInvalidConversationMaintenanceRequest, request.Kind)
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: candidate inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Limit <= 0:
		return fmt.Errorf("%w: candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	case request.AfterActivity.IsZero() != (request.AfterRootID == ""):
		return fmt.Errorf("%w: candidate cursor requires both activity and root id", ErrInvalidConversationMaintenanceRequest)
	default:
		return nil
	}
}
