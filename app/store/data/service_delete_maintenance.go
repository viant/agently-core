package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
)

// ConversationMaintenanceKind separates independently configurable cleanup
// policies. The operation verifies the kind again inside its transaction.
type ConversationMaintenanceKind string

const (
	ConversationMaintenanceInteractive       ConversationMaintenanceKind = "interactive"
	ConversationMaintenanceScheduled         ConversationMaintenanceKind = "scheduled"
	ConversationMaintenanceScheduledFallback ConversationMaintenanceKind = "scheduled_fallback"
)

// ConversationMaintenanceMode is explicit so a caller cannot accidentally
// turn a dry run into a deletion by omitting a boolean option.
type ConversationMaintenanceMode string

const (
	ConversationMaintenanceDryRun ConversationMaintenanceMode = "dry_run"
	ConversationMaintenanceDelete ConversationMaintenanceMode = "delete"
)

// ConversationMaintenanceReason is a stable classification for candidates
// that are intentionally skipped rather than failed by an infrastructure
// error.
type ConversationMaintenanceReason string

const (
	ConversationMaintenanceEligible             ConversationMaintenanceReason = "eligible"
	ConversationMaintenanceDeleted              ConversationMaintenanceReason = "deleted"
	ConversationMaintenanceNotFound             ConversationMaintenanceReason = "not_found"
	ConversationMaintenanceNotRoot              ConversationMaintenanceReason = "not_root"
	ConversationMaintenanceOwnerMissing         ConversationMaintenanceReason = "owner_missing"
	ConversationMaintenanceOwnerMismatch        ConversationMaintenanceReason = "owner_mismatch"
	ConversationMaintenanceRelatedOwnerMismatch ConversationMaintenanceReason = "related_owner_mismatch"
	ConversationMaintenanceKindMismatch         ConversationMaintenanceReason = "kind_mismatch"
	ConversationMaintenanceRecentActivity       ConversationMaintenanceReason = "recent_activity"
	ConversationMaintenanceActivityUnknown      ConversationMaintenanceReason = "activity_unknown"
	ConversationMaintenanceRunPresent           ConversationMaintenanceReason = "run_present"
	ConversationMaintenanceLiveRun              ConversationMaintenanceReason = "live_run"
	ConversationMaintenanceLiveSchedule         ConversationMaintenanceReason = "live_schedule"
	ConversationMaintenanceActiveReportExport   ConversationMaintenanceReason = "active_report_export"
	ConversationMaintenanceGraphReferenced      ConversationMaintenanceReason = "graph_referenced"
	ConversationMaintenanceScheduleReferenced   ConversationMaintenanceReason = "schedule_referenced"
	ConversationMaintenanceScheduleMissing      ConversationMaintenanceReason = "schedule_missing"
	ConversationMaintenanceInternalSchedule     ConversationMaintenanceReason = "internal_schedule"
	ConversationMaintenanceGraphTooLarge        ConversationMaintenanceReason = "graph_too_large"
)

// ConversationMaintenanceRequest describes one root-graph evaluation. The
// expected owner comes from the candidate row selected by maintenance code;
// it is verified across the complete graph and is never injected as request
// authentication.
type ConversationMaintenanceRequest struct {
	RootID          string
	ExpectedOwnerID string
	Kind            ConversationMaintenanceKind
	InactiveBefore  time.Time
	Mode            ConversationMaintenanceMode
	Lease           MaintenanceLease
}

type ConversationMaintenanceResult struct {
	RootID            string
	Kind              ConversationMaintenanceKind
	Mode              ConversationMaintenanceMode
	Eligible          bool
	Deleted           bool
	Reason            ConversationMaintenanceReason
	ConversationCount int
	LastActivity      time.Time
}

func (s *datlyService) MaintainConversationTree(ctx context.Context, request ConversationMaintenanceRequest) (*ConversationMaintenanceResult, error) {
	request.RootID = strings.TrimSpace(request.RootID)
	request.ExpectedOwnerID = strings.TrimSpace(request.ExpectedOwnerID)
	request.InactiveBefore = request.InactiveBefore.UTC()
	if err := validateConversationMaintenanceRequest(request); err != nil {
		return nil, err
	}

	result := &ConversationMaintenanceResult{
		RootID: request.RootID,
		Kind:   request.Kind,
		Mode:   request.Mode,
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		return struct{}{}, s.maintainConversationTreeDirect(ctx, request, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func validateConversationMaintenanceRequest(request ConversationMaintenanceRequest) error {
	switch {
	case request.RootID == "":
		return fmt.Errorf("%w: root id is required", ErrInvalidConversationMaintenanceRequest)
	case request.ExpectedOwnerID == "" && request.Kind != ConversationMaintenanceScheduledFallback:
		return fmt.Errorf("%w: expected owner id is required", ErrInvalidConversationMaintenanceRequest)
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Kind != ConversationMaintenanceInteractive && request.Kind != ConversationMaintenanceScheduled && request.Kind != ConversationMaintenanceScheduledFallback:
		return fmt.Errorf("%w: unsupported kind %q", ErrInvalidConversationMaintenanceRequest, request.Kind)
	case request.Mode != ConversationMaintenanceDryRun && request.Mode != ConversationMaintenanceDelete:
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidConversationMaintenanceRequest, request.Mode)
	case request.Mode == ConversationMaintenanceDelete:
		if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
			return fmt.Errorf("%w: delete mode requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
		}
	default:
		return nil
	}
	return nil
}

func (s *datlyService) maintainConversationTreeDirect(ctx context.Context, request ConversationMaintenanceRequest, result *ConversationMaintenanceResult) error {
	now := time.Now().UTC()
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return err
	}
	if request.Mode == ConversationMaintenanceDelete {
		if _, err := lockMaintenanceLeaseTx(ctx, tx, driver, request.Lease); err != nil {
			return err
		}
	}
	isRoot, found, err := conversationMaintenanceRootState(ctx, tx, request.RootID)
	if err != nil {
		return err
	}
	if !found {
		result.Reason = ConversationMaintenanceNotFound
		return nil
	}
	if !isRoot {
		result.Reason = ConversationMaintenanceNotRoot
		return nil
	}

	graph, err := buildConversationDeleteGraph(ctx, tx, []string{request.RootID}, capabilities)
	if err != nil {
		if errors.Is(err, ErrConversationNotFound) {
			result.Reason = ConversationMaintenanceNotFound
			return nil
		}
		if errors.Is(err, ErrConversationGraphTooLarge) {
			result.Reason = ConversationMaintenanceGraphTooLarge
			return nil
		}
		return err
	}
	result.ConversationCount = len(graph.ConversationIDs)

	if request.Mode == ConversationMaintenanceDelete {
		if err := lockConversationGraphForDelete(ctx, tx, graph); err != nil {
			return err
		}
	}
	if request.Kind != ConversationMaintenanceScheduledFallback {
		if reason := conversationMaintenanceOwnerReason(graph.Rows, request.ExpectedOwnerID); reason != "" {
			result.Reason = reason
			return nil
		}
	}

	actualKind, err := conversationMaintenanceGraphKind(ctx, tx, graph)
	if err != nil {
		return err
	}
	expectedKind := request.Kind
	if expectedKind == ConversationMaintenanceScheduledFallback {
		expectedKind = ConversationMaintenanceScheduled
	}
	if actualKind != expectedKind {
		result.Reason = ConversationMaintenanceKindMismatch
		return nil
	}

	lastActivity, known, err := conversationMaintenanceLastActivity(ctx, tx, graph.ConversationIDs)
	if err != nil {
		return err
	}
	result.LastActivity = lastActivity
	if !known {
		result.Reason = ConversationMaintenanceActivityUnknown
		return nil
	}
	if lastActivity.After(request.InactiveBefore) {
		result.Reason = ConversationMaintenanceRecentActivity
		return nil
	}

	if request.Kind == ConversationMaintenanceScheduledFallback {
		hasRuns, err := conversationMaintenanceGraphHasRunRecords(ctx, tx, graph)
		if err != nil {
			return err
		}
		if hasRuns {
			result.Reason = ConversationMaintenanceRunPresent
			return nil
		}
	}

	prepareGraph := func() error {
		if request.Kind == ConversationMaintenanceScheduledFallback {
			return prepareConversationDeleteGraphForSystemMaintenance(ctx, tx, graph, now)
		}
		return prepareConversationDeleteGraph(ctx, tx, graph, request.ExpectedOwnerID, now)
	}
	if err := prepareGraph(); err != nil {
		switch {
		case errors.Is(err, ErrPermissionDenied):
			result.Reason = ConversationMaintenanceRelatedOwnerMismatch
		case errors.Is(err, ErrConversationGraphReferenced):
			result.Reason = ConversationMaintenanceGraphReferenced
		case errors.Is(err, ErrConversationScheduleReferenced):
			result.Reason = ConversationMaintenanceScheduleReferenced
		case errors.Is(err, ErrConversationActive):
			result.Reason = ConversationMaintenanceLiveSchedule
		default:
			return err
		}
		return nil
	}
	if err := refreshConversationDeleteRunIDs(ctx, tx, graph); err != nil {
		return err
	}
	if request.Kind == ConversationMaintenanceScheduledFallback {
		hasRuns, err := conversationMaintenanceGraphHasRunRecords(ctx, tx, graph)
		if err != nil {
			return err
		}
		if hasRuns {
			result.Reason = ConversationMaintenanceRunPresent
			return nil
		}
	}
	if request.Mode == ConversationMaintenanceDelete {
		if err := lockConversationRunRowsForDelete(ctx, tx, graph); err != nil {
			return err
		}
	}

	if err := ensureNoLiveConversationRuns(ctx, tx, graph, now); err != nil {
		if errors.Is(err, ErrConversationActive) {
			result.Reason = ConversationMaintenanceLiveRun
			return nil
		}
		return err
	}
	if err := ensureNoActiveReportExports(ctx, tx, graph); err != nil {
		if errors.Is(err, ErrConversationActive) {
			result.Reason = ConversationMaintenanceActiveReportExport
			return nil
		}
		return err
	}

	result.Eligible = true
	if request.Mode == ConversationMaintenanceDryRun {
		result.Reason = ConversationMaintenanceEligible
		return nil
	}
	if err := deleteConversationGraph(ctx, tx, graph); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	result.Deleted = true
	result.Reason = ConversationMaintenanceDeleted
	return nil
}

// conversationMaintenanceGraphHasRunRecords is intentionally stricter than
// the ordinary liveness check. The scheduled-conversation fallback only owns
// historical shells which are not represented by either the current run
// table or the legacy schedule_run table. A stale schedule_run_id marker alone
// does not count unless the referenced row still exists.
func conversationMaintenanceGraphHasRunRecords(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) (bool, error) {
	if graph == nil {
		return false, nil
	}
	runIDs, err := collectRunIDsForDelete(ctx, tx, graph.ConversationIDs, graph.TurnIDs)
	if err != nil {
		return false, err
	}
	if len(runIDs) > 0 {
		return true, nil
	}
	if graph.Capabilities == nil || !graph.Capabilities.hasTable("schedule_run") {
		return false, nil
	}
	scheduleRunIDs, err := collectScheduleRunIDsForDelete(ctx, tx, graph.Rows, graph.ConversationIDs, graph.Capabilities)
	if err != nil {
		return false, err
	}
	for _, chunk := range chunkStrings(scheduleRunIDs, deleteChunkSize) {
		query := fmt.Sprintf("SELECT 1 FROM schedule_run WHERE id IN (%s) LIMIT 1", placeholders(len(chunk)))
		var marker int
		err = tx.QueryRowContext(ctx, query, stringArgs(chunk)...).Scan(&marker)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, sql.ErrNoRows):
		default:
			return false, err
		}
	}
	return false, nil
}

func conversationMaintenanceRootState(ctx context.Context, tx *sql.Tx, rootID string) (isRoot bool, found bool, err error) {
	var parentID, parentTurnID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT conversation_parent_id, conversation_parent_turn_id FROM conversation WHERE id = ?`, rootID).Scan(&parentID, &parentTurnID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return strings.TrimSpace(parentID.String) == "" && strings.TrimSpace(parentTurnID.String) == "", true, nil
}

func conversationMaintenanceOwnerReason(rows map[string]*conversationTreeRow, expectedOwnerID string) ConversationMaintenanceReason {
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.OwnerID) == "" {
			return ConversationMaintenanceOwnerMissing
		}
		if strings.TrimSpace(row.OwnerID) != expectedOwnerID {
			return ConversationMaintenanceOwnerMismatch
		}
	}
	return ""
}

func conversationMaintenanceGraphKind(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) (ConversationMaintenanceKind, error) {
	for _, chunk := range chunkStrings(graph.ConversationIDs, deleteChunkSize) {
		query := fmt.Sprintf(`SELECT 1 FROM conversation
WHERE id IN (%s)
  AND (COALESCE(scheduled, 0) <> 0
       OR TRIM(COALESCE(schedule_id, '')) <> ''
       OR TRIM(COALESCE(schedule_run_id, '')) <> ''
       OR TRIM(COALESCE(schedule_kind, '')) <> '')
LIMIT 1`, placeholders(len(chunk)))
		var marker int
		err := tx.QueryRowContext(ctx, query, stringArgs(chunk)...).Scan(&marker)
		switch {
		case err == nil:
			return ConversationMaintenanceScheduled, nil
		case errors.Is(err, sql.ErrNoRows):
		default:
			return "", err
		}
	}
	if len(graph.ScheduleRunIDs) > 0 {
		return ConversationMaintenanceScheduled, nil
	}
	if scheduled, err := hasStatusInSet(ctx, tx, "SELECT conversation_kind FROM run WHERE id IN (%s)", graph.RunIDs, statusSet("scheduled")); err != nil {
		return "", err
	} else if scheduled {
		return ConversationMaintenanceScheduled, nil
	}
	return ConversationMaintenanceInteractive, nil
}

func conversationMaintenanceLastActivity(ctx context.Context, tx *sql.Tx, ids []string) (time.Time, bool, error) {
	var latest time.Time
	seen := 0
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		query := fmt.Sprintf(`SELECT CAST(COALESCE(last_activity, updated_at, created_at) AS CHAR)
FROM conversation WHERE id IN (%s)`, placeholders(len(chunk)))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			return time.Time{}, false, err
		}
		for rows.Next() {
			var raw sql.NullString
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return time.Time{}, false, err
			}
			parsed, ok := parseDBTime(raw.String)
			if !raw.Valid || !ok {
				_ = rows.Close()
				return time.Time{}, false, nil
			}
			seen++
			if latest.IsZero() || parsed.After(latest) {
				latest = parsed
			}
		}
		if err := rows.Close(); err != nil {
			return time.Time{}, false, err
		}
	}
	return latest, seen == len(ids) && seen > 0, nil
}
