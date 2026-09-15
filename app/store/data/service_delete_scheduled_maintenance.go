package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ScheduledRunMaintenanceSource identifies the persisted scheduler-run
// representation. MySQL installations can still contain legacy schedule_run
// rows; SQLite uses only run.
type ScheduledRunMaintenanceSource string

const (
	ScheduledRunMaintenanceCurrent ScheduledRunMaintenanceSource = "run"
	ScheduledRunMaintenanceLegacy  ScheduledRunMaintenanceSource = "schedule_run"
)

type ScheduledRunMaintenanceCandidateRequest struct {
	InactiveBefore time.Time
	AfterActivity  time.Time
	AfterRunID     string
	Limit          int
}

// ScheduledRunMaintenanceCandidate contains only bounded metadata used by the
// cleanup worker. It deliberately excludes prompts, messages and payloads.
type ScheduledRunMaintenanceCandidate struct {
	RunID string
	// ExpectedOwnerID is a diagnostic snapshot. System retention does not use
	// historical owner columns as an authorization boundary.
	ExpectedOwnerID string
	ActivityAt      time.Time
}

type ScheduledRunMaintenanceRequest struct {
	RunID string
	// ExpectedOwnerID is retained for caller compatibility and diagnostics.
	// It is intentionally not enforced by system scheduled-run retention.
	ExpectedOwnerID string
	InactiveBefore  time.Time
	Mode            ConversationMaintenanceMode
	Lease           MaintenanceLease
}

type ScheduledRunMaintenanceResult struct {
	RunID             string
	ScheduleID        string
	Source            ScheduledRunMaintenanceSource
	Mode              ConversationMaintenanceMode
	Eligible          bool
	Deleted           bool
	Reason            ConversationMaintenanceReason
	ConversationCount int
	LastActivity      time.Time
}

type scheduledRunMaintenanceRow struct {
	ID              string
	ScheduleID      string
	ConversationID  string
	EffectiveUserID string
	Source          ScheduledRunMaintenanceSource
	ActivityAt      time.Time
}

func (s *datlyService) ListScheduledRunMaintenanceCandidates(ctx context.Context, request ScheduledRunMaintenanceCandidateRequest) ([]ScheduledRunMaintenanceCandidate, error) {
	request.InactiveBefore = request.InactiveBefore.UTC()
	request.AfterActivity = request.AfterActivity.UTC()
	request.AfterRunID = strings.TrimSpace(request.AfterRunID)
	if err := validateScheduledRunMaintenanceCandidateRequest(request); err != nil {
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

	sources := make([]string, 0, 2)
	args := make([]interface{}, 0, 9)
	currentActivity := "COALESCE(r.completed_at, r.updated_at, r.created_at)"
	currentSource, currentArgs := scheduledRunMaintenanceCandidateSource(
		"run r",
		"r.id",
		"r.schedule_id",
		"r.effective_user_id",
		currentActivity,
		"",
		request,
	)
	sources = append(sources, currentSource)
	args = append(args, currentArgs...)

	if capabilities.hasTable("schedule_run") {
		legacyActivity := "COALESCE(sr.completed_at, sr.updated_at, sr.created_at)"
		legacySource, legacyArgs := scheduledRunMaintenanceCandidateSource(
			"schedule_run sr",
			"sr.id",
			"sr.schedule_id",
			"NULL",
			legacyActivity,
			"AND NOT EXISTS (SELECT 1 FROM run current_run WHERE current_run.id = sr.id)",
			request,
		)
		sources = append(sources, legacySource)
		args = append(args, legacyArgs...)
	}

	query := fmt.Sprintf(`SELECT candidate_id, owner_id, CAST(activity_at AS CHAR)
FROM (%s) maintenance_candidate
ORDER BY activity_at ASC, candidate_id ASC
LIMIT ?`, strings.Join(sources, "\nUNION ALL\n"))
	args = append(args, request.Limit)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]ScheduledRunMaintenanceCandidate, 0, request.Limit)
	for rows.Next() {
		var candidate ScheduledRunMaintenanceCandidate
		var rawActivity sql.NullString
		if err := rows.Scan(&candidate.RunID, &candidate.ExpectedOwnerID, &rawActivity); err != nil {
			return nil, err
		}
		candidate.RunID = strings.TrimSpace(candidate.RunID)
		candidate.ExpectedOwnerID = strings.TrimSpace(candidate.ExpectedOwnerID)
		activityAt, ok := parseDBTime(rawActivity.String)
		if !rawActivity.Valid || !ok {
			return nil, fmt.Errorf("scheduled maintenance candidate %q has invalid activity time %q", candidate.RunID, rawActivity.String)
		}
		candidate.ActivityAt = activityAt
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func scheduledRunMaintenanceCandidateSource(tableExpr, idExpr, scheduleIDExpr, fallbackOwnerExpr, activityExpr, extraPredicate string, request ScheduledRunMaintenanceCandidateRequest) (string, []interface{}) {
	ownerExpr := fmt.Sprintf("TRIM(COALESCE(NULLIF(TRIM(s.created_by_user_id), ''), %s, ''))", fallbackOwnerExpr)
	args := []interface{}{request.InactiveBefore}
	cursorPredicate := ""
	if request.AfterRunID != "" {
		cursorPredicate = fmt.Sprintf("AND (%s > ? OR (%s = ? AND %s > ?))", activityExpr, activityExpr, idExpr)
		args = append(args, request.AfterActivity, request.AfterActivity, request.AfterRunID)
	}
	return fmt.Sprintf(`SELECT %s AS candidate_id, %s AS owner_id, %s AS activity_at
FROM %s
JOIN schedule s ON s.id = %s
WHERE TRIM(COALESCE(%s, '')) <> ''
  AND %s IS NOT NULL
  AND %s <= ?
  %s
  %s`, idExpr, ownerExpr, activityExpr, tableExpr, scheduleIDExpr, scheduleIDExpr, activityExpr, activityExpr, cursorPredicate, extraPredicate), args
}

func validateScheduledRunMaintenanceCandidateRequest(request ScheduledRunMaintenanceCandidateRequest) error {
	switch {
	case request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: scheduled-run candidate inactivity cutoff is required", ErrInvalidConversationMaintenanceRequest)
	case request.Limit <= 0:
		return fmt.Errorf("%w: scheduled-run candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	case request.AfterActivity.IsZero() != (request.AfterRunID == ""):
		return fmt.Errorf("%w: scheduled-run candidate cursor requires both activity and run id", ErrInvalidConversationMaintenanceRequest)
	default:
		return nil
	}
}

// MaintainScheduledRun evaluates scheduler retention and, in delete mode,
// rechecks and deletes the run in the same fenced transaction.
func (s *datlyService) MaintainScheduledRun(ctx context.Context, request ScheduledRunMaintenanceRequest) (*ScheduledRunMaintenanceResult, error) {
	request.RunID = strings.TrimSpace(request.RunID)
	request.ExpectedOwnerID = strings.TrimSpace(request.ExpectedOwnerID)
	request.InactiveBefore = request.InactiveBefore.UTC()
	if request.Mode == "" {
		request.Mode = ConversationMaintenanceDryRun
	}
	if err := validateScheduledRunMaintenanceRequest(request); err != nil {
		return nil, err
	}
	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	maintain := func() (*ScheduledRunMaintenanceResult, error) {
		return s.maintainScheduledRunDirect(ctx, db, driver, request)
	}
	if request.Mode == ConversationMaintenanceDelete {
		return maintenanceLeaseWrite(ctx, s.writeGate, driver, maintain)
	}
	return maintain()
}

func validateScheduledRunMaintenanceRequest(request ScheduledRunMaintenanceRequest) error {
	switch {
	case request.RunID == "" || request.InactiveBefore.IsZero():
		return fmt.Errorf("%w: scheduled-run id and inactivity cutoff are required", ErrInvalidConversationMaintenanceRequest)
	case request.Mode != ConversationMaintenanceDryRun && request.Mode != ConversationMaintenanceDelete:
		return fmt.Errorf("%w: unsupported scheduled-run maintenance mode %q", ErrInvalidConversationMaintenanceRequest, request.Mode)
	case request.Mode == ConversationMaintenanceDelete:
		if err := validateMaintenanceLease(normalizeMaintenanceLease(request.Lease)); err != nil {
			return fmt.Errorf("%w: delete mode requires maintenance lease: %v", ErrInvalidConversationMaintenanceRequest, err)
		}
	}
	return nil
}

func (s *datlyService) maintainScheduledRunDirect(ctx context.Context, db *sql.DB, driver string, request ScheduledRunMaintenanceRequest) (*ScheduledRunMaintenanceResult, error) {
	result := &ScheduledRunMaintenanceResult{RunID: request.RunID, Mode: request.Mode}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: request.Mode == ConversationMaintenanceDryRun})
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return nil, err
	}
	if request.Mode == ConversationMaintenanceDelete {
		if _, err := lockMaintenanceLeaseTx(ctx, tx, driver, request.Lease); err != nil {
			return nil, err
		}
	}
	lockRows := request.Mode == ConversationMaintenanceDelete && isMaintenanceMySQLDriver(driver)
	run, found, err := loadScheduledRunForMaintenance(ctx, tx, request.RunID, capabilities, lockRows)
	if err != nil {
		return nil, err
	}
	if !found {
		result.Reason = ConversationMaintenanceNotFound
		return result, nil
	}
	result.ScheduleID = run.ScheduleID
	result.Source = run.Source
	result.LastActivity = run.ActivityAt

	schedule, found, err := loadScheduleForScheduledRunMaintenance(ctx, tx, run.ScheduleID, lockRows)
	if err != nil {
		return nil, err
	}
	if !found {
		result.Reason = ConversationMaintenanceScheduleMissing
		return result, nil
	}
	if schedule.Internal {
		result.Reason = ConversationMaintenanceInternalSchedule
		return result, nil
	}
	deleteRow := &scheduledRunDeleteRow{
		ID:              run.ID,
		ScheduleID:      run.ScheduleID,
		ConversationID:  run.ConversationID,
		EffectiveUserID: run.EffectiveUserID,
		Source:          run.Source,
	}
	rootIDs, err := collectScheduledRunConversationRoots(ctx, tx, deleteRow, capabilities)
	if err != nil {
		return nil, err
	}
	graph := &conversationDeleteGraph{Capabilities: capabilities, Rows: map[string]*conversationTreeRow{}}
	if run.Source == ScheduledRunMaintenanceLegacy {
		graph.ScheduleRunIDs = []string{run.ID}
	} else {
		graph.RunIDs = []string{run.ID}
	}
	if len(rootIDs) > 0 {
		graph, err = buildConversationDeleteGraph(ctx, tx, rootIDs, capabilities)
		if err != nil {
			if errors.Is(err, ErrConversationNotFound) {
				result.Reason = ConversationMaintenanceNotFound
				return result, nil
			}
			if errors.Is(err, ErrConversationGraphTooLarge) {
				result.Reason = ConversationMaintenanceGraphTooLarge
				return result, nil
			}
			return nil, err
		}
		result.ConversationCount = len(graph.ConversationIDs)
		if request.Mode == ConversationMaintenanceDelete {
			if err := lockConversationGraphForDelete(ctx, tx, graph); err != nil {
				return nil, err
			}
		}
		if err := ensureScheduledRunMaintenanceGraphScope(ctx, tx, graph, run); err != nil {
			if errors.Is(err, ErrConversationGraphReferenced) {
				result.Reason = ConversationMaintenanceGraphReferenced
				return result, nil
			}
			return nil, err
		}
		graphActivity, known, err := conversationMaintenanceLastActivity(ctx, tx, graph.ConversationIDs)
		if err != nil {
			return nil, err
		}
		if !known {
			result.Reason = ConversationMaintenanceActivityUnknown
			return result, nil
		}
		result.LastActivity = laterTime(result.LastActivity, graphActivity)
		if err := prepareConversationDeleteGraphForSystemMaintenance(ctx, tx, graph, time.Now().UTC()); err != nil {
			result.Reason, err = scheduledRunMaintenancePrepareReason(err)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}

	if run.Source == ScheduledRunMaintenanceLegacy {
		graph.ScheduleRunIDs = normalizeDeleteIDs(append(graph.ScheduleRunIDs, run.ID))
	} else {
		graph.RunIDs = normalizeDeleteIDs(append(graph.RunIDs, run.ID))
	}
	if err := refreshConversationDeleteRunIDs(ctx, tx, graph); err != nil {
		return nil, err
	}
	if request.Mode == ConversationMaintenanceDelete {
		if err := lockConversationRunRowsForDelete(ctx, tx, graph); err != nil {
			return nil, err
		}
	}
	relatedActivity, known, err := scheduledRunMaintenanceRelatedActivity(ctx, tx, graph)
	if err != nil {
		return nil, err
	}
	if known {
		result.LastActivity = laterTime(result.LastActivity, relatedActivity)
	}
	if result.LastActivity.After(request.InactiveBefore) {
		result.Reason = ConversationMaintenanceRecentActivity
		return result, nil
	}
	if err := ensureNoLiveConversationRuns(ctx, tx, graph, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrConversationActive) {
			result.Reason = ConversationMaintenanceLiveRun
			return result, nil
		}
		return nil, err
	}
	if err := ensureNoActiveReportExports(ctx, tx, graph); err != nil {
		if errors.Is(err, ErrConversationActive) {
			result.Reason = ConversationMaintenanceActiveReportExport
			return result, nil
		}
		return nil, err
	}

	result.Eligible = true
	if request.Mode == ConversationMaintenanceDryRun {
		result.Reason = ConversationMaintenanceEligible
		return result, nil
	}
	if err := deleteConversationGraph(ctx, tx, graph); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	result.Deleted = true
	result.Reason = ConversationMaintenanceDeleted
	return result, nil
}

// ensureScheduledRunMaintenanceGraphScope replaces owner-based authorization
// for system retention with structural containment. A graph may contain
// ordinary nested runs, but it must not be explicitly attached to another
// schedule or another persisted scheduled run.
func ensureScheduledRunMaintenanceGraphScope(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, run *scheduledRunMaintenanceRow) error {
	if graph == nil || graph.Capabilities == nil || run == nil {
		return nil
	}
	targetRunID := strings.TrimSpace(run.ID)
	targetScheduleID := strings.TrimSpace(run.ScheduleID)
	for _, row := range graph.Rows {
		if row == nil {
			continue
		}
		if scheduleRunID := strings.TrimSpace(row.ScheduleRunID); scheduleRunID != "" && scheduleRunID != targetRunID {
			return fmt.Errorf("%w: conversation=%s schedule_run=%s target_run=%s", ErrConversationGraphReferenced, row.ID, scheduleRunID, targetRunID)
		}
	}

	if graph.Capabilities.hasColumn("conversation", "schedule_id") {
		for _, chunk := range chunkStrings(graph.ConversationIDs, deleteChunkSize) {
			query := fmt.Sprintf("SELECT id, COALESCE(schedule_id, '') FROM conversation WHERE id IN (%s)", placeholders(len(chunk)))
			rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var conversationID, scheduleID string
				if err := rows.Scan(&conversationID, &scheduleID); err != nil {
					_ = rows.Close()
					return err
				}
				scheduleID = strings.TrimSpace(scheduleID)
				if scheduleID != "" && scheduleID != targetScheduleID {
					_ = rows.Close()
					return fmt.Errorf("%w: conversation=%s schedule=%s target_schedule=%s", ErrConversationGraphReferenced, conversationID, scheduleID, targetScheduleID)
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
	}

	if err := ensureScheduledRunMaintenanceRowsInScope(ctx, tx, "run", graph.RunIDs, targetRunID, targetScheduleID, false); err != nil {
		return err
	}
	if graph.Capabilities.hasTable("schedule_run") {
		if err := ensureScheduledRunMaintenanceRowsInScope(ctx, tx, "schedule_run", graph.ScheduleRunIDs, targetRunID, targetScheduleID, true); err != nil {
			return err
		}
	}
	return nil
}

func ensureScheduledRunMaintenanceRowsInScope(ctx context.Context, tx *sql.Tx, table string, ids []string, targetRunID, targetScheduleID string, allRowsScheduled bool) error {
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		query := fmt.Sprintf("SELECT id, COALESCE(schedule_id, '') FROM %s WHERE id IN (%s)", table, placeholders(len(chunk)))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var runID, scheduleID string
			if err := rows.Scan(&runID, &scheduleID); err != nil {
				_ = rows.Close()
				return err
			}
			runID = strings.TrimSpace(runID)
			scheduleID = strings.TrimSpace(scheduleID)
			isScheduled := allRowsScheduled || scheduleID != ""
			if isScheduled && (runID != targetRunID || scheduleID != targetScheduleID) {
				_ = rows.Close()
				return fmt.Errorf("%w: %s=%s schedule=%s target_run=%s target_schedule=%s", ErrConversationGraphReferenced, table, runID, scheduleID, targetRunID, targetScheduleID)
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func loadScheduledRunForMaintenance(ctx context.Context, tx *sql.Tx, runID string, capabilities *deleteSchemaCapabilities, lock bool) (*scheduledRunMaintenanceRow, bool, error) {
	row, found, err := queryScheduledRunMaintenanceRow(ctx, tx, `SELECT id, COALESCE(schedule_id, ''), COALESCE(conversation_id, ''), COALESCE(effective_user_id, ''), CAST(COALESCE(completed_at, updated_at, created_at) AS CHAR)
FROM run WHERE id = ?`, runID, ScheduledRunMaintenanceCurrent, lock)
	if err != nil || found || capabilities == nil || !capabilities.hasTable("schedule_run") {
		return row, found, err
	}
	return queryScheduledRunMaintenanceRow(ctx, tx, `SELECT id, COALESCE(schedule_id, ''), COALESCE(conversation_id, ''), '', CAST(COALESCE(completed_at, updated_at, created_at) AS CHAR)
FROM schedule_run WHERE id = ?`, runID, ScheduledRunMaintenanceLegacy, lock)
}

func queryScheduledRunMaintenanceRow(ctx context.Context, tx *sql.Tx, query, runID string, source ScheduledRunMaintenanceSource, lock bool) (*scheduledRunMaintenanceRow, bool, error) {
	if lock {
		query += " FOR UPDATE"
	}
	var row scheduledRunMaintenanceRow
	var rawActivity sql.NullString
	err := tx.QueryRowContext(ctx, query, runID).Scan(&row.ID, &row.ScheduleID, &row.ConversationID, &row.EffectiveUserID, &rawActivity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	activityAt, ok := parseDBTime(rawActivity.String)
	if !rawActivity.Valid || !ok {
		return nil, false, fmt.Errorf("scheduled run %q has invalid activity time %q", runID, rawActivity.String)
	}
	row.ID = strings.TrimSpace(row.ID)
	row.ScheduleID = strings.TrimSpace(row.ScheduleID)
	row.ConversationID = strings.TrimSpace(row.ConversationID)
	row.EffectiveUserID = strings.TrimSpace(row.EffectiveUserID)
	row.Source = source
	row.ActivityAt = activityAt
	return &row, true, nil
}

func loadScheduleForScheduledRunMaintenance(ctx context.Context, tx *sql.Tx, scheduleID string, lock bool) (*scheduledRunScheduleRow, bool, error) {
	if strings.TrimSpace(scheduleID) == "" {
		return nil, false, nil
	}
	var row scheduledRunScheduleRow
	var internal int
	query := `SELECT id, COALESCE(created_by_user_id, ''), COALESCE(internal, 0) FROM schedule WHERE id = ?`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRowContext(ctx, query, scheduleID).Scan(&row.ID, &row.OwnerID, &internal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	row.ID = strings.TrimSpace(row.ID)
	row.OwnerID = strings.TrimSpace(row.OwnerID)
	row.Internal = internal != 0
	return &row, true, nil
}

func scheduledRunMaintenancePrepareReason(err error) (ConversationMaintenanceReason, error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		return ConversationMaintenanceRelatedOwnerMismatch, nil
	case errors.Is(err, ErrConversationGraphReferenced):
		return ConversationMaintenanceGraphReferenced, nil
	case errors.Is(err, ErrConversationScheduleReferenced):
		return ConversationMaintenanceScheduleReferenced, nil
	case errors.Is(err, ErrConversationActive):
		return ConversationMaintenanceLiveSchedule, nil
	default:
		return "", err
	}
}

func scheduledRunMaintenanceRelatedActivity(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) (time.Time, bool, error) {
	var latest time.Time
	known := false
	for _, item := range []struct {
		table string
		ids   []string
	}{
		{table: "run", ids: graph.RunIDs},
		{table: "schedule_run", ids: graph.ScheduleRunIDs},
	} {
		if !graph.Capabilities.hasTable(item.table) {
			continue
		}
		for _, chunk := range chunkStrings(item.ids, deleteChunkSize) {
			query := fmt.Sprintf("SELECT CAST(COALESCE(completed_at, updated_at, created_at) AS CHAR) FROM %s WHERE id IN (%s)", item.table, placeholders(len(chunk)))
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
				known = true
				latest = laterTime(latest, parsed)
			}
			if err := rows.Close(); err != nil {
				return time.Time{}, false, err
			}
		}
	}
	return latest, known, nil
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}
