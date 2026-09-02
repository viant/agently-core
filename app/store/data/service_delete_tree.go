package data

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/sqlitewrite"
)

const (
	deleteChunkSize      = 500
	maxConversationGraph = 10_000
	staleActiveCutoff    = 48 * time.Hour // retained for schedule cascade compatibility
)

type conversationTreeRow struct {
	ID            string
	OwnerID       string
	Status        string
	ScheduleRunID string
	CreatedAt     time.Time
	Depth         int
}

type conversationDeleteGraph struct {
	Rows            map[string]*conversationTreeRow
	ConversationIDs []string
	TurnIDs         []string
	MessageIDs      []string
	RunIDs          []string
	ApprovalIDs     []string
	ScheduleRunIDs  []string
	PayloadIDs      []string
	GoalIDs         []string
	ScheduleIDs     []string
	ReportRunIDs    []string
	ReportJobIDs    []string
	Capabilities    *deleteSchemaCapabilities
}

func (s *datlyService) DeleteConversationTree(ctx context.Context, ids ...string) (retErr error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	ctx, diagnostics := beginConversationDeleteDiagnostics(ctx, ids)
	if diagnostics != nil {
		defer func() {
			diagnostics.finish(retErr)
		}()
	}
	gateStarted := conversationDeleteDiagPhaseStart(ctx, "write_gate_wait")
	gateAcquired := false
	_, retErr = sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		gateAcquired = true
		conversationDeleteDiagPhaseDone(ctx, "write_gate_wait", gateStarted, nil, "")
		return struct{}{}, s.deleteConversationTreeDirect(ctx, ids, time.Now().UTC())
	})
	if !gateAcquired {
		conversationDeleteDiagPhaseDone(ctx, "write_gate_wait", gateStarted, retErr, "")
	}
	return retErr
}

func buildConversationDeleteGraph(ctx context.Context, tx *sql.Tx, rootIDs []string, capabilities *deleteSchemaCapabilities) (*conversationDeleteGraph, error) {
	rows, err := collectConversationTreeWithCapabilities(ctx, tx, rootIDs, capabilities)
	if err != nil {
		return nil, err
	}
	graph := &conversationDeleteGraph{Rows: rows, Capabilities: capabilities}
	graph.ConversationIDs = sortedKeys(rows)
	graph.TurnIDs, err = queryStringsForColumn(ctx, tx, "SELECT id FROM turn WHERE conversation_id IN (%s)", graph.ConversationIDs)
	if err != nil {
		return nil, err
	}
	graph.MessageIDs, err = queryStringsForColumn(ctx, tx, "SELECT id FROM message WHERE conversation_id IN (%s)", graph.ConversationIDs)
	if err != nil {
		return nil, err
	}
	graph.RunIDs, err = collectRunIDsForDelete(ctx, tx, graph.ConversationIDs, graph.TurnIDs)
	if err != nil {
		return nil, err
	}
	graph.ApprovalIDs, err = collectApprovalIDsForDelete(ctx, tx, graph.ConversationIDs, graph.TurnIDs, graph.MessageIDs)
	if err != nil {
		return nil, err
	}
	graph.ScheduleRunIDs, err = collectScheduleRunIDsForDelete(ctx, tx, rows, graph.ConversationIDs, capabilities)
	if err != nil {
		return nil, err
	}
	graph.PayloadIDs, err = collectPayloadIDsForDelete(ctx, tx, graph)
	if err != nil {
		return nil, err
	}
	return graph, nil
}

func collectConversationTree(ctx context.Context, tx *sql.Tx, rootIDs []string) (map[string]*conversationTreeRow, error) {
	return collectConversationTreeWithCapabilities(ctx, tx, rootIDs, nil)
}

func collectConversationTreeWithCapabilities(ctx context.Context, tx *sql.Tx, rootIDs []string, capabilities *deleteSchemaCapabilities) (map[string]*conversationTreeRow, error) {
	if len(rootIDs) > maxConversationGraph {
		return nil, fmt.Errorf("%w: limit=%d", ErrConversationGraphTooLarge, maxConversationGraph)
	}
	rootRows, err := loadConversationsByIDs(ctx, tx, rootIDs, 0)
	if err != nil {
		return nil, err
	}
	if len(rootRows) != len(rootIDs) {
		return nil, ErrConversationNotFound
	}
	rows := map[string]*conversationTreeRow{}
	frontier := make([]string, 0, len(rootRows))
	for _, row := range rootRows {
		copied := row
		rows[row.ID] = &copied
		frontier = append(frontier, row.ID)
	}

	depth := 0
	for len(frontier) > 0 {
		depth++
		children, err := loadChildConversations(ctx, tx, frontier, depth)
		if err != nil {
			return nil, err
		}
		var turnChildren []conversationTreeRow
		if capabilities == nil || capabilities.hasColumn("conversation", "conversation_parent_turn_id") {
			turnChildren, err = loadChildConversationsByParentTurns(ctx, tx, frontier, depth)
			if err != nil {
				return nil, err
			}
		}
		linked, err := loadLinkedConversations(ctx, tx, frontier, depth)
		if err != nil {
			return nil, err
		}
		next := make([]string, 0, len(children)+len(turnChildren)+len(linked))
		candidates := append(children, turnChildren...)
		candidates = append(candidates, linked...)
		for _, row := range candidates {
			if _, ok := rows[row.ID]; ok {
				continue
			}
			if len(rows) >= maxConversationGraph {
				return nil, fmt.Errorf("%w: limit=%d", ErrConversationGraphTooLarge, maxConversationGraph)
			}
			copied := row
			rows[row.ID] = &copied
			next = append(next, row.ID)
		}
		frontier = next
	}
	return rows, nil
}

func loadConversationsByIDs(ctx context.Context, tx *sql.Tx, ids []string, depth int) ([]conversationTreeRow, error) {
	query := `SELECT id, COALESCE(created_by_user_id, ''), COALESCE(status, ''), COALESCE(schedule_run_id, ''), CAST(created_at AS CHAR) FROM conversation WHERE id IN (%s)`
	return queryConversationRows(ctx, tx, query, ids, depth)
}

func loadChildConversations(ctx context.Context, tx *sql.Tx, parentIDs []string, depth int) ([]conversationTreeRow, error) {
	query := `SELECT id, COALESCE(created_by_user_id, ''), COALESCE(status, ''), COALESCE(schedule_run_id, ''), CAST(created_at AS CHAR) FROM conversation WHERE conversation_parent_id IN (%s)`
	return queryConversationRows(ctx, tx, query, parentIDs, depth)
}

func loadChildConversationsByParentTurns(ctx context.Context, tx *sql.Tx, parentConversationIDs []string, depth int) ([]conversationTreeRow, error) {
	turnIDs, err := queryStringsForColumn(ctx, tx, "SELECT id FROM turn WHERE conversation_id IN (%s)", parentConversationIDs)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, COALESCE(created_by_user_id, ''), COALESCE(status, ''), COALESCE(schedule_run_id, ''), CAST(created_at AS CHAR) FROM conversation WHERE conversation_parent_turn_id IN (%s)`
	return queryConversationRows(ctx, tx, query, turnIDs, depth)
}

func loadLinkedConversations(ctx context.Context, tx *sql.Tx, conversationIDs []string, depth int) ([]conversationTreeRow, error) {
	query := `SELECT DISTINCT c.id, COALESCE(c.created_by_user_id, ''), COALESCE(c.status, ''), COALESCE(c.schedule_run_id, ''), CAST(c.created_at AS CHAR)
FROM message m
JOIN conversation c ON c.id = m.linked_conversation_id
WHERE m.conversation_id IN (%s)
  AND COALESCE(m.linked_conversation_id, '') <> ''`
	return queryConversationRows(ctx, tx, query, conversationIDs, depth)
}

func queryConversationRows(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string, depth int) ([]conversationTreeRow, error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	var result []conversationTreeRow
	chunks := chunkStrings(ids, deleteChunkSize)
	for chunkIndex, chunk := range chunks {
		query := fmt.Sprintf(queryTemplate, placeholders(len(chunk)))
		started := conversationDeleteDiagSQLStart(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), 0, -1, started, err)
			return nil, err
		}
		rowCount := 0
		for rows.Next() {
			var row conversationTreeRow
			var rawCreated sql.NullString
			row.Depth = depth
			if err := rows.Scan(&row.ID, &row.OwnerID, &row.Status, &row.ScheduleRunID, &rawCreated); err != nil {
				_ = rows.Close()
				conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
				return nil, err
			}
			if parsed, ok := parseDBTime(rawCreated.String); ok {
				row.CreatedAt = parsed
			}
			result = append(result, row)
			rowCount++
		}
		if err := rows.Close(); err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
			return nil, err
		}
		conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, nil)
	}
	return result, nil
}

func authorizeConversationTreeDelete(rows map[string]*conversationTreeRow, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrPermissionDenied
	}
	for _, row := range rows {
		if row == nil || strings.TrimSpace(row.OwnerID) == "" || strings.TrimSpace(row.OwnerID) != userID {
			return ErrPermissionDenied
		}
	}
	return nil
}

func ensureConversationTreeNotRecentActive(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, now time.Time) error {
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
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(updated_at, last_activity, created_at) AS CHAR) FROM conversation WHERE id IN (%s)`, graph.ConversationIDs, conversationDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(updated_at, created_at) AS CHAR) FROM message WHERE id IN (%s)`, graph.MessageIDs, messageDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(created_at AS CHAR) FROM turn WHERE id IN (%s)`, graph.TurnIDs, turnDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(updated_at, created_at) AS CHAR) FROM turn_queue WHERE turn_id IN (%s)`, graph.TurnIDs, queueDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(last_heartbeat_at, updated_at, started_at, created_at) AS CHAR) FROM run WHERE id IN (%s)`, graph.RunIDs, runDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(started_at, completed_at) AS CHAR) FROM model_call WHERE message_id IN (%s)`, graph.MessageIDs, modelCallDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(started_at, completed_at) AS CHAR) FROM tool_call WHERE message_id IN (%s)`, graph.MessageIDs, toolCallDeleteActiveStatuses(), now, consider); err != nil {
		return err
	}
	if err := scanActiveStatusRows(ctx, tx, `SELECT status, CAST(COALESCE(updated_at, created_at) AS CHAR) FROM tool_approval_queue WHERE id IN (%s)`, graph.ApprovalIDs, approvalDeleteActiveStatuses(), now, consider); err != nil {
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

func scanActiveStatusRows(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string, active map[string]struct{}, now time.Time, consider func(time.Time, bool)) error {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	chunks := chunkStrings(ids, deleteChunkSize)
	for chunkIndex, chunk := range chunks {
		query := fmt.Sprintf(queryTemplate, placeholders(len(chunk)))
		started := conversationDeleteDiagSQLStart(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), 0, -1, started, err)
			return err
		}
		rowCount := 0
		for rows.Next() {
			var status sql.NullString
			var rawTime sql.NullString
			if err := rows.Scan(&status, &rawTime); err != nil {
				_ = rows.Close()
				conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
				return err
			}
			rowCount++
			if _, ok := active[normalizeStatus(status.String)]; !ok {
				continue
			}
			parsed, ok := parseDBTime(rawTime.String)
			if !rawTime.Valid || !ok {
				parsed = now
				ok = false
			}
			consider(parsed, ok)
		}
		if err := rows.Close(); err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
			return err
		}
		conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, nil)
	}
	return nil
}

func deleteConversationGraph(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	capabilities := graph.Capabilities
	if capabilities == nil {
		return fmt.Errorf("conversation deletion schema capabilities are unavailable")
	}
	if err := applyInvestigationDeletePolicy(ctx, tx, graph, conversationInvestigationPolicy); err != nil {
		return err
	}
	if capabilities.hasTable("report_export_artifact") {
		if err := execIDs(ctx, tx, "DELETE FROM report_export_artifact WHERE job_id IN (%s)", graph.ReportJobIDs); err != nil {
			return err
		}
	}
	if capabilities.hasTable("report_export_job") {
		if err := execIDs(ctx, tx, "DELETE FROM report_export_job WHERE job_id IN (%s)", graph.ReportJobIDs); err != nil {
			return err
		}
	}
	if capabilities.hasTable("conversation_report_context") {
		if err := execIDs(ctx, tx, "DELETE FROM conversation_report_context WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
			return err
		}
	}
	if capabilities.hasTable("report_run") {
		if err := execIDs(ctx, tx, "DELETE FROM report_run WHERE report_run_id IN (%s)", graph.ReportRunIDs); err != nil {
			return err
		}
	}
	if capabilities.hasTable("schedule_run") {
		if capabilities.hasColumn("schedule_run", "conversation_id") {
			if err := execIDs(ctx, tx, "DELETE FROM schedule_run WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
				return err
			}
		}
		if err := execIDs(ctx, tx, "DELETE FROM schedule_run WHERE id IN (%s)", graph.ScheduleRunIDs); err != nil {
			return err
		}
	}
	if err := execDeleteIDs(ctx, tx, "tool_approval_queue", graph.ApprovalIDs); err != nil {
		return err
	}
	if capabilities.hasColumn("tool_execution_claim", "turn_id") {
		if err := execIDs(ctx, tx, "DELETE FROM tool_execution_claim WHERE turn_id IN (%s)", graph.TurnIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("run", "resumed_from_run_id") {
		if err := execIDs(ctx, tx, "UPDATE run SET resumed_from_run_id = NULL WHERE resumed_from_run_id IN (%s)", graph.RunIDs); err != nil {
			return err
		}
	}
	if err := execDeleteIDs(ctx, tx, "run", graph.RunIDs); err != nil {
		return err
	}
	if err := execIDs(ctx, tx, "DELETE FROM turn_queue WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
		return err
	}
	if err := execIDs(ctx, tx, "DELETE FROM model_call WHERE message_id IN (%s)", graph.MessageIDs); err != nil {
		return err
	}
	if err := execIDs(ctx, tx, "DELETE FROM tool_call WHERE message_id IN (%s)", graph.MessageIDs); err != nil {
		return err
	}
	if err := execIDs(ctx, tx, "DELETE FROM generated_file WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
		return err
	}
	if capabilities.hasColumn("message", "parent_message_id") {
		if err := execIDs(ctx, tx, "UPDATE message SET parent_message_id = NULL WHERE parent_message_id IN (%s)", graph.MessageIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("message", "superseded_by") {
		if err := execIDs(ctx, tx, "UPDATE message SET superseded_by = NULL WHERE superseded_by IN (%s)", graph.MessageIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("turn", "started_by_message_id") {
		if err := execIDs(ctx, tx, "UPDATE turn SET started_by_message_id = NULL WHERE started_by_message_id IN (%s)", graph.MessageIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("turn", "retry_of") {
		if err := execIDs(ctx, tx, "UPDATE turn SET retry_of = NULL WHERE retry_of IN (%s)", graph.TurnIDs); err != nil {
			return err
		}
	}
	if err := execDeleteIDs(ctx, tx, "message", graph.MessageIDs); err != nil {
		return err
	}
	if err := execDeleteIDs(ctx, tx, "turn", graph.TurnIDs); err != nil {
		return err
	}
	if capabilities.hasTable("schedule") {
		if err := execDeleteIDs(ctx, tx, "schedule", graph.ScheduleIDs); err != nil {
			return err
		}
	}
	if capabilities.hasTable("goal") {
		if err := execDeleteIDs(ctx, tx, "goal", graph.GoalIDs); err != nil {
			return err
		}
	}
	for _, ids := range conversationIDsByDepthDesc(graph.Rows) {
		if err := execDeleteIDs(ctx, tx, "conversation", ids); err != nil {
			return err
		}
	}
	if err := deleteUnreferencedPayloads(ctx, tx, graph.PayloadIDs); err != nil {
		return err
	}
	return nil
}

func applyInvestigationDeletePolicy(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, policy investigationDeletePolicy) error {
	capabilities := graph.Capabilities
	if capabilities.hasColumn("investigation", "conversation_id") {
		switch policy {
		case investigationRetainAndDetach:
			return execIDs(ctx, tx, "UPDATE investigation SET conversation_id = NULL WHERE conversation_id IN (%s)", graph.ConversationIDs)
		case investigationDelete:
			return execIDs(ctx, tx, "DELETE FROM investigation WHERE conversation_id IN (%s)", graph.ConversationIDs)
		}
	}
	return nil
}

func collectRunIDsForDelete(ctx context.Context, tx *sql.Tx, conversationIDs, turnIDs []string) ([]string, error) {
	values := map[string]struct{}{}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM run WHERE conversation_id IN (%s)", conversationIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM run WHERE turn_id IN (%s)", turnIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT run_id FROM turn WHERE id IN (%s)", turnIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT run_id FROM model_call WHERE turn_id IN (%s)", turnIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT run_id FROM tool_call WHERE turn_id IN (%s)", turnIDs); err != nil {
		return nil, err
	}
	return sortedKeys(values), nil
}

func collectApprovalIDsForDelete(ctx context.Context, tx *sql.Tx, conversationIDs, turnIDs, messageIDs []string) ([]string, error) {
	values := map[string]struct{}{}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM tool_approval_queue WHERE conversation_id IN (%s)", conversationIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM tool_approval_queue WHERE turn_id IN (%s)", turnIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM tool_approval_queue WHERE message_id IN (%s)", messageIDs); err != nil {
		return nil, err
	}
	return sortedKeys(values), nil
}

func collectScheduleRunIDsForDelete(ctx context.Context, tx *sql.Tx, rows map[string]*conversationTreeRow, conversationIDs []string, capabilities *deleteSchemaCapabilities) ([]string, error) {
	values := map[string]struct{}{}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if id := strings.TrimSpace(row.ScheduleRunID); id != "" {
			values[id] = struct{}{}
		}
	}
	if capabilities != nil && capabilities.hasColumn("schedule_run", "conversation_id") {
		if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM schedule_run WHERE conversation_id IN (%s)", conversationIDs); err != nil {
			return nil, err
		}
	}
	return sortedKeys(values), nil
}

func collectPayloadIDsForDelete(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) ([]string, error) {
	values := map[string]struct{}{}
	payloadQueries := []struct {
		query       string
		ids         []string
		columnCount int
	}{
		{
			query:       "SELECT attachment_payload_id, elicitation_payload_id FROM message WHERE conversation_id IN (%s)",
			ids:         graph.ConversationIDs,
			columnCount: 2,
		},
		{
			query:       "SELECT request_payload_id, response_payload_id, provider_request_payload_id, provider_response_payload_id, stream_payload_id FROM model_call WHERE message_id IN (%s)",
			ids:         graph.MessageIDs,
			columnCount: 5,
		},
		{
			query:       "SELECT request_payload_id, response_payload_id FROM tool_call WHERE message_id IN (%s)",
			ids:         graph.MessageIDs,
			columnCount: 2,
		},
		{
			query:       "SELECT payload_id FROM generated_file WHERE conversation_id IN (%s)",
			ids:         graph.ConversationIDs,
			columnCount: 1,
		},
	}
	for _, item := range payloadQueries {
		if err := addStringsFromMultiColumnQuery(ctx, tx, values, item.query, item.ids, item.columnCount); err != nil {
			return nil, err
		}
	}
	return sortedKeys(values), nil
}

func addStringsFromMultiColumnQuery(ctx context.Context, tx *sql.Tx, target map[string]struct{}, queryTemplate string, ids []string, columnCount int) error {
	rows, err := queryStringsForColumns(ctx, tx, queryTemplate, ids, columnCount)
	if err != nil {
		return err
	}
	for _, row := range rows {
		target[row] = struct{}{}
	}
	return nil
}

func addStringsFromQuery(ctx context.Context, tx *sql.Tx, target map[string]struct{}, queryTemplate string, ids []string) error {
	rows, err := queryStringsForColumn(ctx, tx, queryTemplate, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		target[row] = struct{}{}
	}
	return nil
}

func queryStringsForColumn(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) ([]string, error) {
	return queryStringsForColumns(ctx, tx, queryTemplate, ids, 1)
}

func queryStringsForColumns(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string, columnCount int) ([]string, error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if columnCount <= 0 {
		return nil, fmt.Errorf("query string column count must be positive")
	}
	values := map[string]struct{}{}
	chunks := chunkStrings(ids, deleteChunkSize)
	for chunkIndex, chunk := range chunks {
		query := fmt.Sprintf(queryTemplate, placeholders(len(chunk)))
		started := conversationDeleteDiagSQLStart(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), 0, -1, started, err)
			return nil, err
		}
		rowCount := 0
		rowValues := make([]sql.NullString, columnCount)
		destinations := make([]interface{}, columnCount)
		for i := range rowValues {
			destinations[i] = &rowValues[i]
		}
		for rows.Next() {
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
				return nil, err
			}
			rowCount++
			for _, value := range rowValues {
				if value.Valid {
					if normalized := strings.TrimSpace(value.String); normalized != "" {
						values[normalized] = struct{}{}
					}
				}
			}
		}
		if err := rows.Close(); err != nil {
			conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, err)
			return nil, err
		}
		conversationDeleteDiagSQLDone(ctx, "query", query, len(chunk), chunkIndex+1, len(chunks), rowCount, -1, started, nil)
	}
	return sortedKeys(values), nil
}

func execDeleteIDs(ctx context.Context, tx *sql.Tx, table string, ids []string) error {
	return execIDs(ctx, tx, fmt.Sprintf("DELETE FROM %s WHERE id IN (%%s)", table), ids)
}

func execIDs(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) error {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	chunks := chunkStrings(ids, deleteChunkSize)
	for chunkIndex, chunk := range chunks {
		query := fmt.Sprintf(queryTemplate, placeholders(len(chunk)))
		started := conversationDeleteDiagSQLStart(ctx, "exec", query, len(chunk), chunkIndex+1, len(chunks))
		result, err := tx.ExecContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			conversationDeleteDiagSQLDone(ctx, "exec", query, len(chunk), chunkIndex+1, len(chunks), 0, -1, started, err)
			return err
		}
		affected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			affected = -1
		}
		conversationDeleteDiagSQLDone(ctx, "exec", query, len(chunk), chunkIndex+1, len(chunks), 0, affected, started, nil)
	}
	return nil
}

func deleteUnreferencedPayloads(ctx context.Context, tx *sql.Tx, ids []string) error {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	queryTemplate := `
DELETE FROM call_payload
WHERE id IN (%s)
  AND NOT EXISTS (
    SELECT 1 FROM message
    WHERE attachment_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM message
    WHERE elicitation_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE request_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE response_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE provider_request_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE provider_response_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE stream_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM tool_call
    WHERE request_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM tool_call
    WHERE response_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM generated_file
    WHERE payload_id = call_payload.id
  )`
	return execIDs(ctx, tx, queryTemplate, ids)
}

func conversationIDsByDepthDesc(rows map[string]*conversationTreeRow) [][]string {
	byDepth := map[int][]string{}
	depths := make([]int, 0)
	for _, row := range rows {
		if row == nil {
			continue
		}
		if _, ok := byDepth[row.Depth]; !ok {
			depths = append(depths, row.Depth)
		}
		byDepth[row.Depth] = append(byDepth[row.Depth], row.ID)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(depths)))
	result := make([][]string, 0, len(depths))
	for _, depth := range depths {
		ids := normalizeDeleteIDs(byDepth[depth])
		sort.SliceStable(ids, func(i, j int) bool {
			left := rows[ids[i]]
			right := rows[ids[j]]
			if left != nil && right != nil && !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return ids[i] < ids[j]
		})
		result = append(result, ids)
	}
	return result
}

func normalizeDeleteIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func sortedKeys[T any](values map[string]T) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for key := range values {
		if key = strings.TrimSpace(key); key != "" {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func chunkStrings(values []string, size int) [][]string {
	if size <= 0 {
		size = deleteChunkSize
	}
	values = normalizeDeleteIDs(values)
	if len(values) == 0 {
		return nil
	}
	var chunks [][]string
	for len(values) > 0 {
		n := size
		if len(values) < n {
			n = len(values)
		}
		chunks = append(chunks, values[:n])
		values = values[n:]
	}
	return chunks
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringArgs(values []string) []interface{} {
	args := make([]interface{}, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func normalizeStatus(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func statusSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = normalizeStatus(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func conversationDeleteActiveStatuses() map[string]struct{} {
	return statusSet("running", "in_progress", "processing", "queued", "pending", "thinking", "streaming", "waiting_for_user", "prechecking", "executing", "open")
}

func messageDeleteActiveStatuses() map[string]struct{} {
	return statusSet("pending", "open", "running", "in_progress", "processing")
}

func turnDeleteActiveStatuses() map[string]struct{} {
	return statusSet("queued", "pending", "running", "waiting_for_user")
}

func queueDeleteActiveStatuses() map[string]struct{} {
	return statusSet("queued", "pending", "running")
}

func runDeleteActiveStatuses() map[string]struct{} {
	return statusSet("pending", "prechecking", "queued", "running")
}

func modelCallDeleteActiveStatuses() map[string]struct{} {
	return statusSet("thinking", "streaming", "running")
}

func toolCallDeleteActiveStatuses() map[string]struct{} {
	return statusSet("queued", "running", "waiting_for_user")
}

func approvalDeleteActiveStatuses() map[string]struct{} {
	return statusSet("pending")
}

func parseDBTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "0000-00-00 00:00:00") {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
