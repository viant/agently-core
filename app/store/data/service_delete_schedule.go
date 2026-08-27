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

var ErrScheduleNotFound = errors.New("schedule not found")

type scheduleDeleteRow struct {
	ID      string
	OwnerID string
}

type scheduleConversationCandidate struct {
	ID        string
	ParentID  string
	CreatedAt time.Time
}

func (s *datlyService) DeleteScheduleCascade(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		db, driver, err := s.dbWithDriver()
		if err != nil {
			return struct{}{}, err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return struct{}{}, err
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()
		if err := deleteScheduleCascadeTx(ctx, tx, id, driver, time.Now().UTC()); err != nil {
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

func deleteScheduleCascadeTx(ctx context.Context, tx *sql.Tx, scheduleID, driver string, now time.Time) error {
	capabilities, err := loadDeleteSchemaCapabilities(ctx, tx, driver)
	if err != nil {
		return err
	}
	row, err := loadScheduleForDelete(ctx, tx, scheduleID)
	if err != nil {
		return err
	}
	userID := strings.TrimSpace(auth.EffectiveUserID(ctx))
	if owner := strings.TrimSpace(row.OwnerID); owner != "" && (userID == "" || userID != owner) {
		return ErrPermissionDenied
	}

	rootIDs, err := collectScheduleConversationRoots(ctx, tx, scheduleID, capabilities)
	if err != nil {
		return err
	}
	graph := &conversationDeleteGraph{Rows: map[string]*conversationTreeRow{}}
	if len(rootIDs) > 0 {
		graph, err = buildConversationDeleteGraph(ctx, tx, rootIDs, capabilities)
		if err != nil {
			return err
		}
		if err := authorizeConversationTreeDelete(graph.Rows, userID); err != nil {
			return err
		}
		if err := ensureConversationTreeNotRecentActive(ctx, tx, graph, now); err != nil {
			return err
		}
	}

	remainingRunIDs, err := collectRemainingScheduleRunIDs(ctx, tx, scheduleID, graph.ConversationIDs)
	if err != nil {
		return err
	}
	if err := ensureScheduleRunsNotRecentActive(ctx, tx, remainingRunIDs, now); err != nil {
		return err
	}
	if len(rootIDs) > 0 {
		if err := deleteConversationGraph(ctx, tx, graph); err != nil {
			return err
		}
	}
	if err := execDeleteIDs(ctx, tx, "run", remainingRunIDs); err != nil {
		return err
	}
	if capabilities.hasColumn("schedule_run", "schedule_id") {
		if err := execIDs(ctx, tx, "DELETE FROM schedule_run WHERE schedule_id IN (%s)", []string{scheduleID}); err != nil {
			return err
		}
	}
	if err := execDeleteIDs(ctx, tx, "schedule", []string{scheduleID}); err != nil {
		return err
	}
	return nil
}

func loadScheduleForDelete(ctx context.Context, tx *sql.Tx, scheduleID string) (*scheduleDeleteRow, error) {
	var row scheduleDeleteRow
	err := tx.QueryRowContext(ctx, `SELECT id, COALESCE(created_by_user_id, '') FROM schedule WHERE id = ?`, scheduleID).Scan(&row.ID, &row.OwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ErrScheduleNotFound, scheduleID)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func collectScheduleConversationRoots(ctx context.Context, tx *sql.Tx, scheduleID string, capabilities *deleteSchemaCapabilities) ([]string, error) {
	ids := map[string]struct{}{}
	if err := addStringsFromQuery(ctx, tx, ids, "SELECT id FROM conversation WHERE schedule_id IN (%s)", []string{scheduleID}); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, ids, "SELECT conversation_id FROM run WHERE schedule_id IN (%s)", []string{scheduleID}); err != nil {
		return nil, err
	}
	if capabilities.hasColumn("schedule_run", "schedule_id") && capabilities.hasColumn("schedule_run", "conversation_id") {
		if err := addStringsFromQuery(ctx, tx, ids, "SELECT conversation_id FROM schedule_run WHERE schedule_id IN (%s)", []string{scheduleID}); err != nil {
			return nil, err
		}
	}
	if len(ids) == 0 {
		return nil, nil
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

func loadScheduleConversationCandidates(ctx context.Context, tx *sql.Tx, ids []string) ([]scheduleConversationCandidate, error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	var result []scheduleConversationCandidate
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT id, COALESCE(conversation_parent_id, ''), CAST(created_at AS CHAR) FROM conversation WHERE id IN (%s)`, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var candidate scheduleConversationCandidate
			var rawCreated sql.NullString
			if err := rows.Scan(&candidate.ID, &candidate.ParentID, &rawCreated); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if parsed, ok := parseDBTime(rawCreated.String); ok {
				candidate.CreatedAt = parsed
			}
			result = append(result, candidate)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func collectRemainingScheduleRunIDs(ctx context.Context, tx *sql.Tx, scheduleID string, conversationIDs []string) ([]string, error) {
	values := map[string]struct{}{}
	conversationIDSet := map[string]struct{}{}
	for _, conversationID := range normalizeDeleteIDs(conversationIDs) {
		conversationIDSet[conversationID] = struct{}{}
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, COALESCE(conversation_id, '') FROM run WHERE schedule_id = ?", scheduleID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var conversationID string
		if err := rows.Scan(&id, &conversationID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		normalizedID := strings.TrimSpace(id)
		if normalizedID == "" {
			continue
		}
		normalizedConversationID := strings.TrimSpace(conversationID)
		if _, deletedWithConversation := conversationIDSet[normalizedConversationID]; deletedWithConversation {
			continue
		}
		values[normalizedID] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return sortedKeys(values), nil
}

func ensureScheduleRunsNotRecentActive(ctx context.Context, tx *sql.Tx, runIDs []string, now time.Time) error {
	var latestActive time.Time
	activeFound := false
	consider := func(ts time.Time, ok bool) {
		if !ok {
			ts = now
		}
		ts = ts.UTC()
		if !activeFound || ts.After(latestActive) {
			latestActive = ts
		}
		activeFound = true
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(last_heartbeat_at, updated_at, started_at, created_at) AS CHAR) FROM run WHERE id IN (%s)`, runIDs, runDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if !activeFound {
		return nil
	}
	if latestActive.After(now.Add(-staleActiveCutoff)) {
		return ErrConversationActive
	}
	return nil
}
