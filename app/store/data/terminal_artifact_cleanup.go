package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
	agtoolcallwrite "github.com/viant/agently-core/pkg/agently/toolcall/write"
)

// TerminalArtifactCleanupStore is an optional, narrow startup-maintenance
// capability. It deliberately is not part of Service so callers that do not
// use the Datly store do not need to expose direct SQL maintenance methods.
type TerminalArtifactCleanupStore interface {
	SnapshotTerminalArtifactCandidates(ctx context.Context, terminalSince time.Time, terminalTurnLimit int) ([]TerminalArtifactCandidate, error)
	CleanupTerminalArtifactCandidates(ctx context.Context, candidates []TerminalArtifactCandidate, completedAt time.Time) ([]TerminalArtifactDisposition, error)
}

var _ TerminalArtifactCleanupStore = (*datlyService)(nil)

type TerminalArtifactKind string

const (
	TerminalArtifactModelCall TerminalArtifactKind = "model_call"
	TerminalArtifactToolCall  TerminalArtifactKind = "tool_call"
	TerminalArtifactMessage   TerminalArtifactKind = "message"
)

type TerminalArtifactLinkage string

const (
	TerminalArtifactDirectTurn  TerminalArtifactLinkage = "direct_turn"
	TerminalArtifactMessageTurn TerminalArtifactLinkage = "message_turn"
	TerminalArtifactRun         TerminalArtifactLinkage = "run"
	TerminalArtifactLegacyRun   TerminalArtifactLinkage = "legacy_run"
)

// TerminalArtifactCandidate contains only the identity and optimistic guard
// values captured at startup. Message content and artifact payloads are never
// retained in memory.
type TerminalArtifactCandidate struct {
	Kind           TerminalArtifactKind
	ID             string
	ConversationID string
	TurnID         string
	Linkage        TerminalArtifactLinkage
	ExpectedLink   string
	ExpectedRun    string
	TerminalStatus string
	Reason         string
}

type TerminalArtifactDisposition uint8

const (
	TerminalArtifactUnresolved TerminalArtifactDisposition = iota
	TerminalArtifactRepaired
	TerminalArtifactAlreadyResolved
	TerminalArtifactNoLongerEligible
)

const terminalArtifactSnapshotSQL = `
WITH terminal_turns AS (
    SELECT t.id, t.conversation_id, t.run_id, t.status, t.error_message, t.created_at
    FROM turn t
    WHERE t.created_at >= ?
      AND LOWER(TRIM(t.status)) IN ('failed', 'succeeded', 'canceled')
    ORDER BY t.created_at DESC, t.id DESC
    LIMIT ?
),
execution_artifacts AS (
    SELECT 'model_call' AS artifact_kind,
           mc.message_id AS artifact_id,
           mc.turn_id AS artifact_turn_id,
           mc.run_id AS artifact_run_id
    FROM model_call mc
    WHERE LOWER(TRIM(COALESCE(mc.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
    UNION ALL
    SELECT 'tool_call' AS artifact_kind,
           tc.message_id AS artifact_id,
           tc.turn_id AS artifact_turn_id,
           tc.run_id AS artifact_run_id
    FROM tool_call tc
    WHERE LOWER(TRIM(COALESCE(tc.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
),
candidate_rows AS (
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'direct_turn' AS link_mode,
           COALESCE(a.artifact_turn_id, '') AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           1 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.id = a.artifact_turn_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'message_turn' AS link_mode,
           COALESCE(m.turn_id, '') AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           2 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN message m ON m.id = a.artifact_id
    JOIN terminal_turns tt ON tt.id = m.turn_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'run' AS link_mode,
           '' AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           3 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.run_id IS NOT NULL
                          AND tt.run_id <> ''
                          AND tt.run_id = a.artifact_run_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'legacy_run' AS link_mode,
           '' AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           4 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.id = a.artifact_run_id
    UNION ALL
    SELECT 'message' AS artifact_kind, m.id AS artifact_id,
           tt.conversation_id, tt.id AS turn_id,
           'direct_turn' AS link_mode,
           COALESCE(m.turn_id, '') AS expected_link,
           '' AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           1 AS link_rank, tt.created_at AS terminal_created_at
    FROM message m
    JOIN terminal_turns tt ON tt.id = m.turn_id
    WHERE (LOWER(TRIM(m.role)) = 'tool' OR LOWER(TRIM(m.type)) = 'tool_op')
      AND LOWER(TRIM(COALESCE(m.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
)
SELECT artifact_kind, artifact_id, conversation_id, turn_id, link_mode,
       expected_link, expected_run, terminal_status, terminal_error, link_rank
FROM candidate_rows
ORDER BY artifact_kind ASC, artifact_id ASC, link_rank ASC,
         terminal_created_at DESC, turn_id DESC`

func (s *datlyService) SnapshotTerminalArtifactCandidates(ctx context.Context, terminalSince time.Time, terminalTurnLimit int) ([]TerminalArtifactCandidate, error) {
	if terminalTurnLimit <= 0 {
		return nil, fmt.Errorf("terminal turn limit must be positive")
	}
	if terminalTurnLimit > 5000 {
		terminalTurnLimit = 5000
	}
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, terminalArtifactSnapshotSQL, terminalSince.UTC(), terminalTurnLimit)
	if err != nil {
		return nil, fmt.Errorf("query terminal artifact snapshot: %w", err)
	}
	defer rows.Close()

	result := make([]TerminalArtifactCandidate, 0)
	seen := map[string]struct{}{}
	for rows.Next() {
		var (
			candidate TerminalArtifactCandidate
			kind      string
			linkage   string
			turnError string
			linkRank  int
		)
		if err := rows.Scan(
			&kind,
			&candidate.ID,
			&candidate.ConversationID,
			&candidate.TurnID,
			&linkage,
			&candidate.ExpectedLink,
			&candidate.ExpectedRun,
			&candidate.TerminalStatus,
			&turnError,
			&linkRank,
		); err != nil {
			return nil, fmt.Errorf("scan terminal artifact snapshot: %w", err)
		}
		candidate.Kind = TerminalArtifactKind(kind)
		candidate.Linkage = TerminalArtifactLinkage(linkage)
		candidate.TerminalStatus = strings.ToLower(strings.TrimSpace(candidate.TerminalStatus))
		candidate.Reason = strings.TrimSpace(turnError)
		if candidate.Reason == "" {
			candidate.Reason = fmt.Sprintf("turn reached terminal status %s", candidate.TerminalStatus)
		}
		key := string(candidate.Kind) + "\x00" + candidate.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read terminal artifact snapshot: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close terminal artifact snapshot: %w", err)
	}
	return result, nil
}

func (s *datlyService) CleanupTerminalArtifactCandidates(ctx context.Context, candidates []TerminalArtifactCandidate, completedAt time.Time) ([]TerminalArtifactDisposition, error) {
	if len(candidates) == 0 {
		return []TerminalArtifactDisposition{}, nil
	}
	return sqlitewrite.Do(ctx, s.writeGate, func() ([]TerminalArtifactDisposition, error) {
		db, err := s.db()
		if err != nil {
			return nil, err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("begin terminal artifact cleanup: %w", err)
		}
		committed := false
		defer func() {
			if !committed {
				_ = tx.Rollback()
			}
		}()

		dispositions, err := cleanupTerminalArtifactCandidatesTx(ctx, tx, candidates, completedAt.UTC())
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit terminal artifact cleanup: %w", err)
		}
		committed = true
		return dispositions, nil
	})
}

type terminalArtifactUpdate struct {
	query string
	args  []any
}

func cleanupTerminalArtifactCandidatesTx(ctx context.Context, tx *sql.Tx, candidates []TerminalArtifactCandidate, completedAt time.Time) ([]TerminalArtifactDisposition, error) {
	dispositions := make([]TerminalArtifactDisposition, len(candidates))
	statements := map[string]*sql.Stmt{}
	for i, candidate := range candidates {
		update, err := buildTerminalArtifactUpdate(candidate, completedAt)
		if err != nil {
			return nil, err
		}
		statementKey := string(candidate.Kind) + "\x00" + string(candidate.Linkage)
		stmt := statements[statementKey]
		if stmt == nil {
			stmt, err = tx.PrepareContext(ctx, update.query)
			if err != nil {
				return nil, fmt.Errorf("prepare terminal artifact update: %w", err)
			}
			statements[statementKey] = stmt
		}
		result, err := stmt.ExecContext(ctx, update.args...)
		if err != nil {
			return nil, fmt.Errorf("update terminal artifact: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("read terminal artifact update result: %w", err)
		}
		if affected == 1 {
			dispositions[i] = TerminalArtifactRepaired
			continue
		}
		if affected != 0 {
			return nil, fmt.Errorf("terminal artifact update affected an unexpected number of rows")
		}
		disposition, err := classifyTerminalArtifactCandidate(ctx, tx, candidate)
		if err != nil {
			return nil, err
		}
		dispositions[i] = disposition
	}
	return dispositions, nil
}

const (
	terminalArtifactNonterminalSQL = "LOWER(TRIM(COALESCE(%s.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')"
	terminalTurnTerminalSQL        = "LOWER(TRIM(t.status)) IN ('failed', 'succeeded', 'canceled') AND LOWER(TRIM(t.status)) = ?"
)

func buildTerminalArtifactUpdate(candidate TerminalArtifactCandidate, completedAt time.Time) (*terminalArtifactUpdate, error) {
	if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.TurnID) == "" || strings.TrimSpace(candidate.ConversationID) == "" {
		return nil, fmt.Errorf("terminal artifact candidate identity is incomplete")
	}
	expectedTerminalStatus := strings.ToLower(strings.TrimSpace(candidate.TerminalStatus))
	if !isTerminalTurnCleanupStatus(expectedTerminalStatus) {
		return nil, fmt.Errorf("terminal artifact candidate status is invalid")
	}
	table := ""
	switch candidate.Kind {
	case TerminalArtifactModelCall:
		table = "model_call"
	case TerminalArtifactToolCall:
		table = "tool_call"
	case TerminalArtifactMessage:
		table = "message"
		if candidate.Linkage != TerminalArtifactDirectTurn {
			return nil, fmt.Errorf("terminal artifact candidate kind/linkage is invalid")
		}
	default:
		return nil, fmt.Errorf("terminal artifact candidate kind is invalid")
	}

	setSQL := "status = 'failed', error_message = ?, completed_at = ?"
	args := make([]any, 0, 8)
	if candidate.Kind == TerminalArtifactMessage {
		setSQL = "status = 'failed', updated_at = ?"
		args = append(args, completedAt)
	} else {
		reason := candidate.Reason
		if candidate.Kind == TerminalArtifactToolCall {
			if candidate.Linkage == TerminalArtifactMessageTurn {
				reason = "tool message terminalized after turn ended"
			}
			reason = agtoolcallwrite.SanitizeErrorMessage(reason)
		}
		args = append(args, reason, completedAt)
	}
	args = append(args, candidate.ID)

	where := fmt.Sprintf("%s.id = ?", table)
	if candidate.Kind != TerminalArtifactMessage {
		where = fmt.Sprintf("%s.message_id = ?", table)
	}
	where += " AND " + fmt.Sprintf(terminalArtifactNonterminalSQL, table)

	switch candidate.Linkage {
	case TerminalArtifactDirectTurn:
		if candidate.ExpectedLink == "" || candidate.ExpectedLink != candidate.TurnID {
			return nil, fmt.Errorf("terminal artifact candidate direct linkage is invalid")
		}
		where += fmt.Sprintf(" AND %s.turn_id = ? AND EXISTS (SELECT 1 FROM turn t WHERE t.id = ? AND t.conversation_id = ? AND %s)", table, terminalTurnTerminalSQL)
		args = append(args, candidate.ExpectedLink, candidate.TurnID, candidate.ConversationID, expectedTerminalStatus)
	case TerminalArtifactMessageTurn:
		if candidate.Kind == TerminalArtifactMessage || candidate.ExpectedLink == "" || candidate.ExpectedLink != candidate.TurnID {
			return nil, fmt.Errorf("terminal artifact candidate message linkage is invalid")
		}
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM message m JOIN turn t ON t.id = m.turn_id WHERE m.id = %s.message_id AND m.turn_id = ? AND t.id = ? AND t.conversation_id = ? AND %s)", table, terminalTurnTerminalSQL)
		args = append(args, candidate.ExpectedLink, candidate.TurnID, candidate.ConversationID, expectedTerminalStatus)
	case TerminalArtifactRun:
		if candidate.Kind == TerminalArtifactMessage || candidate.ExpectedRun == "" {
			return nil, fmt.Errorf("terminal artifact candidate run linkage is invalid")
		}
		where += fmt.Sprintf(" AND %s.run_id = ? AND EXISTS (SELECT 1 FROM turn t WHERE t.id = ? AND t.conversation_id = ? AND t.run_id = ? AND %s)", table, terminalTurnTerminalSQL)
		args = append(args, candidate.ExpectedRun, candidate.TurnID, candidate.ConversationID, candidate.ExpectedRun, expectedTerminalStatus)
	case TerminalArtifactLegacyRun:
		if candidate.Kind == TerminalArtifactMessage || candidate.ExpectedRun == "" || candidate.ExpectedRun != candidate.TurnID {
			return nil, fmt.Errorf("terminal artifact candidate legacy linkage is invalid")
		}
		where += fmt.Sprintf(" AND %s.run_id = ? AND EXISTS (SELECT 1 FROM turn t WHERE t.id = ? AND t.conversation_id = ? AND %s)", table, terminalTurnTerminalSQL)
		args = append(args, candidate.ExpectedRun, candidate.TurnID, candidate.ConversationID, expectedTerminalStatus)
	default:
		return nil, fmt.Errorf("terminal artifact candidate linkage is invalid")
	}
	return &terminalArtifactUpdate{query: "UPDATE " + table + " SET " + setSQL + " WHERE " + where, args: args}, nil
}

type terminalArtifactState struct {
	status string
	turnID string
	runID  string
}

type terminalTurnState struct {
	status         string
	conversationID string
	runID          string
}

func classifyTerminalArtifactCandidate(ctx context.Context, tx *sql.Tx, candidate TerminalArtifactCandidate) (TerminalArtifactDisposition, error) {
	artifact, found, err := loadTerminalArtifactState(ctx, tx, candidate.Kind, candidate.ID)
	if err != nil {
		return TerminalArtifactUnresolved, err
	}
	if !found || isTerminalArtifactCleanupStatus(artifact.status) {
		return TerminalArtifactAlreadyResolved, nil
	}
	turn, found, err := loadTerminalTurnState(ctx, tx, candidate.TurnID)
	if err != nil {
		return TerminalArtifactUnresolved, err
	}
	expectedTerminalStatus := strings.ToLower(strings.TrimSpace(candidate.TerminalStatus))
	if !found ||
		!isTerminalTurnCleanupStatus(turn.status) ||
		strings.ToLower(strings.TrimSpace(turn.status)) != expectedTerminalStatus ||
		turn.conversationID != candidate.ConversationID {
		return TerminalArtifactNoLongerEligible, nil
	}

	eligible := false
	switch candidate.Linkage {
	case TerminalArtifactDirectTurn:
		eligible = candidate.ExpectedLink == candidate.TurnID && artifact.turnID == candidate.ExpectedLink
	case TerminalArtifactMessageTurn:
		messageTurnID, messageFound, err := loadTerminalArtifactMessageTurn(ctx, tx, candidate.ID)
		if err != nil {
			return TerminalArtifactUnresolved, err
		}
		eligible = messageFound && candidate.ExpectedLink == candidate.TurnID && messageTurnID == candidate.ExpectedLink
	case TerminalArtifactRun:
		eligible = candidate.ExpectedRun != "" && artifact.runID == candidate.ExpectedRun && turn.runID == candidate.ExpectedRun
	case TerminalArtifactLegacyRun:
		eligible = candidate.ExpectedRun == candidate.TurnID && artifact.runID == candidate.ExpectedRun
	}
	if !eligible {
		return TerminalArtifactNoLongerEligible, nil
	}
	return TerminalArtifactUnresolved, nil
}

func loadTerminalArtifactState(ctx context.Context, tx *sql.Tx, kind TerminalArtifactKind, id string) (terminalArtifactState, bool, error) {
	var state terminalArtifactState
	query := ""
	switch kind {
	case TerminalArtifactModelCall:
		query = "SELECT COALESCE(status, ''), COALESCE(turn_id, ''), COALESCE(run_id, '') FROM model_call WHERE message_id = ?"
	case TerminalArtifactToolCall:
		query = "SELECT COALESCE(status, ''), COALESCE(turn_id, ''), COALESCE(run_id, '') FROM tool_call WHERE message_id = ?"
	case TerminalArtifactMessage:
		query = "SELECT COALESCE(status, ''), COALESCE(turn_id, ''), '' FROM message WHERE id = ?"
	default:
		return state, false, fmt.Errorf("terminal artifact candidate kind is invalid")
	}
	err := tx.QueryRowContext(ctx, query, id).Scan(&state.status, &state.turnID, &state.runID)
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	if err != nil {
		return state, false, fmt.Errorf("load terminal artifact state: %w", err)
	}
	return state, true, nil
}

func loadTerminalTurnState(ctx context.Context, tx *sql.Tx, id string) (terminalTurnState, bool, error) {
	var state terminalTurnState
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(status, ''), COALESCE(conversation_id, ''), COALESCE(run_id, '') FROM turn WHERE id = ?", id).
		Scan(&state.status, &state.conversationID, &state.runID)
	if errors.Is(err, sql.ErrNoRows) {
		return state, false, nil
	}
	if err != nil {
		return state, false, fmt.Errorf("load terminal turn state: %w", err)
	}
	return state, true, nil
}

func loadTerminalArtifactMessageTurn(ctx context.Context, tx *sql.Tx, messageID string) (string, bool, error) {
	var turnID string
	err := tx.QueryRowContext(ctx, "SELECT COALESCE(turn_id, '') FROM message WHERE id = ?", messageID).Scan(&turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("load terminal artifact message linkage: %w", err)
	}
	return turnID, true, nil
}

func isTerminalArtifactCleanupStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "failed", "canceled", "cancelled", "succeeded":
		return true
	default:
		return false
	}
}

func isTerminalTurnCleanupStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "succeeded", "canceled":
		return true
	default:
		return false
	}
}
