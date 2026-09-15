package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sqlitewrite"
)

type scheduledRunDeleteRow struct {
	ID              string
	ScheduleID      string
	ConversationID  string
	EffectiveUserID string
	Source          ScheduledRunMaintenanceSource
}

type scheduledRunScheduleRow struct {
	ID       string
	OwnerID  string
	Internal bool
}

// DeleteScheduledRun deletes one persisted scheduler run and the complete
// conversation graph created by it. The schedule definition is retained.
func (s *datlyService) DeleteScheduledRun(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("%w: empty id", ErrScheduledRunNotFound)
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		db, driver, err := s.dbWithDriver()
		if err != nil {
			return struct{}{}, err
		}
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return struct{}{}, err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if err := deleteScheduledRunTx(ctx, tx, id, driver, time.Now().UTC()); err != nil {
			return struct{}{}, err
		}
		if err := tx.Commit(); err != nil {
			return struct{}{}, err
		}
		committed = true
		return struct{}{}, nil
	})
	return err
}

func deleteScheduledRunTx(ctx context.Context, tx *sql.Tx, runID, driver string, now time.Time) error {
	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return err
	}
	locatedRun, err := locateScheduledRunForDelete(ctx, tx, runID, capabilities)
	if err != nil {
		return err
	}
	schedule, err := loadScheduledRunSchedule(ctx, tx, locatedRun.ScheduleID, driver)
	if err != nil {
		return err
	}
	if schedule.Internal {
		return fmt.Errorf("%w: %s", ErrScheduledRunNotFound, runID)
	}
	run, err := loadScheduledRunForDelete(ctx, tx, runID, locatedRun.Source, driver)
	if err != nil {
		return err
	}
	if run.ScheduleID != schedule.ID {
		return fmt.Errorf("%w: %s", ErrScheduledRunNotFound, runID)
	}
	userID := strings.TrimSpace(auth.EffectiveUserID(ctx))
	ownerID := strings.TrimSpace(schedule.OwnerID)
	if ownerID == "" {
		ownerID = strings.TrimSpace(run.EffectiveUserID)
	}
	if userID == "" || ownerID == "" || userID != ownerID {
		return ErrPermissionDenied
	}

	rootIDs, err := collectScheduledRunConversationRoots(ctx, tx, run, capabilities)
	if err != nil {
		return err
	}
	if len(rootIDs) == 0 {
		graph := &conversationDeleteGraph{Capabilities: capabilities}
		appendScheduledRunDeleteID(graph, run)
		if err := lockConversationRunRowsForDelete(ctx, tx, graph); err != nil {
			return err
		}
		if err := ensureNoLiveConversationRuns(ctx, tx, graph, now); err != nil {
			return err
		}
		if capabilities.hasColumn("schedule_run", "id") {
			if err := execDeleteIDs(ctx, tx, "schedule_run", graph.ScheduleRunIDs); err != nil {
				return err
			}
		}
		if capabilities.hasColumn("run", "resumed_from_run_id") {
			if err := execIDs(ctx, tx, "UPDATE run SET resumed_from_run_id = NULL WHERE resumed_from_run_id IN (%s)", graph.RunIDs); err != nil {
				return err
			}
		}
		return execDeleteIDs(ctx, tx, "run", graph.RunIDs)
	}

	graph, err := buildConversationDeleteGraph(ctx, tx, rootIDs, capabilities)
	if err != nil {
		return err
	}
	if err := authorizeConversationTreeDelete(graph.Rows, userID); err != nil {
		return err
	}
	if err := lockConversationGraphForDelete(ctx, tx, graph); err != nil {
		return err
	}
	if err := prepareConversationDeleteGraph(ctx, tx, graph, userID, now); err != nil {
		return err
	}
	appendScheduledRunDeleteID(graph, run)
	if err := validateConversationDeleteGraph(ctx, tx, graph, now); err != nil {
		return err
	}
	return deleteConversationGraph(ctx, tx, graph)
}

func appendScheduledRunDeleteID(graph *conversationDeleteGraph, run *scheduledRunDeleteRow) {
	if graph == nil || run == nil {
		return
	}
	if run.Source == ScheduledRunMaintenanceLegacy {
		graph.ScheduleRunIDs = normalizeDeleteIDs(append(graph.ScheduleRunIDs, run.ID))
		return
	}
	graph.RunIDs = normalizeDeleteIDs(append(graph.RunIDs, run.ID))
}

func loadScheduledRunSchedule(ctx context.Context, tx *sql.Tx, scheduleID, driver string) (*scheduledRunScheduleRow, error) {
	query := "SELECT id, COALESCE(created_by_user_id, ''), COALESCE(internal, 0) FROM schedule WHERE id = ?"
	if strings.Contains(strings.ToLower(driver), "mysql") {
		query += " FOR UPDATE"
	}
	var row scheduledRunScheduleRow
	var internal int
	err := tx.QueryRowContext(ctx, query, scheduleID).Scan(&row.ID, &row.OwnerID, &internal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: schedule=%s", ErrScheduledRunNotFound, scheduleID)
	}
	if err != nil {
		return nil, err
	}
	row.ID = strings.TrimSpace(row.ID)
	row.OwnerID = strings.TrimSpace(row.OwnerID)
	row.Internal = internal != 0
	return &row, nil
}

func locateScheduledRunForDelete(ctx context.Context, tx *sql.Tx, runID string, capabilities *deleteSchemaCapabilities) (*scheduledRunDeleteRow, error) {
	row, found, err := queryScheduledRunForDelete(ctx, tx, runID, ScheduledRunMaintenanceCurrent, false)
	if err != nil {
		return nil, err
	}
	if found {
		return row, nil
	}
	if capabilities != nil && capabilities.hasTable("schedule_run") {
		row, found, err = queryScheduledRunForDelete(ctx, tx, runID, ScheduledRunMaintenanceLegacy, false)
		if err != nil {
			return nil, err
		}
		if found {
			return row, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrScheduledRunNotFound, runID)
}

func loadScheduledRunForDelete(ctx context.Context, tx *sql.Tx, runID string, source ScheduledRunMaintenanceSource, driver string) (*scheduledRunDeleteRow, error) {
	row, found, err := queryScheduledRunForDelete(ctx, tx, runID, source, strings.Contains(strings.ToLower(driver), "mysql"))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", ErrScheduledRunNotFound, runID)
	}
	return row, nil
}

func queryScheduledRunForDelete(ctx context.Context, tx *sql.Tx, runID string, source ScheduledRunMaintenanceSource, lock bool) (*scheduledRunDeleteRow, bool, error) {
	query := "SELECT id, COALESCE(schedule_id, ''), COALESCE(conversation_id, ''), COALESCE(effective_user_id, '') FROM run WHERE id = ?"
	if source == ScheduledRunMaintenanceLegacy {
		query = "SELECT id, COALESCE(schedule_id, ''), COALESCE(conversation_id, ''), '' FROM schedule_run WHERE id = ?"
	}
	if lock {
		query += " FOR UPDATE"
	}
	var row scheduledRunDeleteRow
	err := tx.QueryRowContext(ctx, query, runID).Scan(&row.ID, &row.ScheduleID, &row.ConversationID, &row.EffectiveUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	row.ID = strings.TrimSpace(row.ID)
	row.ScheduleID = strings.TrimSpace(row.ScheduleID)
	row.ConversationID = strings.TrimSpace(row.ConversationID)
	row.EffectiveUserID = strings.TrimSpace(row.EffectiveUserID)
	row.Source = source
	if row.ID == "" || row.ScheduleID == "" {
		return nil, false, nil
	}
	return &row, true, nil
}

func collectScheduledRunConversationRoots(ctx context.Context, tx *sql.Tx, run *scheduledRunDeleteRow, capabilities *deleteSchemaCapabilities) ([]string, error) {
	ids := map[string]struct{}{}
	if run.ConversationID != "" {
		ids[run.ConversationID] = struct{}{}
	}
	if capabilities.hasColumn("conversation", "schedule_run_id") {
		if err := addStringsFromQuery(ctx, tx, ids, "SELECT id FROM conversation WHERE schedule_run_id IN (%s)", []string{run.ID}); err != nil {
			return nil, err
		}
	}
	if run.Source == ScheduledRunMaintenanceLegacy && capabilities.hasColumn("schedule_run", "conversation_id") {
		if err := addStringsFromQuery(ctx, tx, ids, "SELECT conversation_id FROM schedule_run WHERE id IN (%s)", []string{run.ID}); err != nil {
			return nil, err
		}
	}
	candidates, err := loadScheduleConversationCandidates(ctx, tx, sortedKeys(ids))
	if err != nil {
		return nil, err
	}
	candidateIDs := map[string]struct{}{}
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = struct{}{}
	}
	roots := make([]scheduleConversationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, parentSelected := candidateIDs[strings.TrimSpace(candidate.ParentID)]; parentSelected {
			continue
		}
		roots = append(roots, candidate)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		if !roots[i].CreatedAt.Equal(roots[j].CreatedAt) {
			return roots[i].CreatedAt.Before(roots[j].CreatedAt)
		}
		return roots[i].ID < roots[j].ID
	})
	result := make([]string, 0, len(roots))
	for _, root := range roots {
		result = append(result, root.ID)
	}
	return result, nil
}
