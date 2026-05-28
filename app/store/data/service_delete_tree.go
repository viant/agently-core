package data

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/auth"
	"github.com/viant/agently-core/internal/sqlitewrite"
	"github.com/viant/datly"
	"github.com/viant/datly/repository"
	"github.com/viant/datly/repository/contract"
	"github.com/viant/xdatly/handler"
	"github.com/viant/xdatly/handler/response"
)

const (
	conversationTreeDeletePathURI = "/v1/api/agently/conversation/delete-tree"
	deleteChunkSize               = 500
	staleActiveCutoff             = 48 * time.Hour
)

type deleteConversationTreeInput struct {
	IDs []string `parameter:",kind=body,in=ids" json:"ids"`
}

type deleteConversationTreeOutput struct {
	response.Status `parameter:",kind=output,in=status" anonymous:"true" json:",omitempty"`
	Deleted         []string `parameter:",kind=body,in=deleted" json:"deleted,omitempty"`
}

type deleteConversationTreeHandler struct{}

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
}

func defineConversationTreeDeleteComponent(ctx context.Context, srv *datly.Service) (*repository.Component, error) {
	return srv.AddHandler(ctx, contract.NewPath(http.MethodDelete, conversationTreeDeletePathURI), &deleteConversationTreeHandler{},
		repository.WithResource(srv.Resource()),
		repository.WithContract(
			reflect.TypeOf(&deleteConversationTreeInput{}),
			reflect.TypeOf(&deleteConversationTreeOutput{}),
			nil,
		),
	)
}

func (s *datlyService) DeleteConversationTree(ctx context.Context, ids ...string) error {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	_, err := sqlitewrite.Do(ctx, s.writeGate, func() (struct{}, error) {
		in := &deleteConversationTreeInput{IDs: ids}
		out := &deleteConversationTreeOutput{}
		_, err := s.dao.Operate(ctx,
			datly.WithPath(contract.NewPath(http.MethodDelete, conversationTreeDeletePathURI)),
			datly.WithInput(in),
			datly.WithOutput(out),
		)
		return struct{}{}, err
	})
	return err
}

func (h *deleteConversationTreeHandler) Exec(ctx context.Context, sess handler.Session) (interface{}, error) {
	out := &deleteConversationTreeOutput{}
	out.Status.Status = "ok"
	if err := h.exec(ctx, sess, out); err != nil {
		out.Status.Status = "error"
		out.Status.Message = err.Error()
		return out, err
	}
	return out, nil
}

func (h *deleteConversationTreeHandler) exec(ctx context.Context, sess handler.Session, out *deleteConversationTreeOutput) error {
	in := &deleteConversationTreeInput{}
	if err := sess.Stater().Bind(ctx, in); err != nil {
		return err
	}
	rootIDs := normalizeDeleteIDs(in.IDs)
	if len(rootIDs) == 0 {
		return nil
	}
	userID := strings.TrimSpace(auth.EffectiveUserID(ctx))
	if userID == "" {
		return ErrPermissionDenied
	}
	sqlxService, err := sess.Db()
	if err != nil {
		return err
	}
	tx, err := sqlxService.Tx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	graph, err := buildConversationDeleteGraph(ctx, tx, rootIDs)
	if err != nil {
		return err
	}
	if err := authorizeConversationTreeDelete(graph.Rows, userID); err != nil {
		return err
	}
	if err := ensureConversationTreeNotRecentActive(ctx, tx, graph, time.Now().UTC()); err != nil {
		return err
	}
	if err := deleteConversationGraph(ctx, tx, graph); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	out.Deleted = graph.ConversationIDs
	return nil
}

func buildConversationDeleteGraph(ctx context.Context, tx *sql.Tx, rootIDs []string) (*conversationDeleteGraph, error) {
	rows, err := collectConversationTree(ctx, tx, rootIDs)
	if err != nil {
		return nil, err
	}
	graph := &conversationDeleteGraph{Rows: rows}
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
	graph.ScheduleRunIDs, err = collectScheduleRunIDsForDelete(ctx, tx, rows, graph.ConversationIDs)
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
		linked, err := loadLinkedConversations(ctx, tx, frontier, depth)
		if err != nil {
			return nil, err
		}
		next := make([]string, 0, len(children)+len(linked))
		for _, row := range append(children, linked...) {
			if _, ok := rows[row.ID]; ok {
				continue
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
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var row conversationTreeRow
			var rawCreated sql.NullString
			row.Depth = depth
			if err := rows.Scan(&row.ID, &row.OwnerID, &row.Status, &row.ScheduleRunID, &rawCreated); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if parsed, ok := parseDBTime(rawCreated.String); ok {
				row.CreatedAt = parsed
			}
			result = append(result, row)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
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
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var status sql.NullString
			var rawTime sql.NullString
			if err := rows.Scan(&status, &rawTime); err != nil {
				_ = rows.Close()
				return err
			}
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
			return err
		}
	}
	return nil
}

func deleteConversationGraph(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	if err := optionalExecIDs(ctx, tx, "UPDATE investigation SET conversation_id = NULL WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
		return err
	}
	if err := optionalExecIDs(ctx, tx, "DELETE FROM schedule_run WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
		return err
	}
	if err := optionalExecIDs(ctx, tx, "DELETE FROM schedule_run WHERE id IN (%s)", graph.ScheduleRunIDs); err != nil {
		return err
	}
	if err := execDeleteIDs(ctx, tx, "tool_approval_queue", graph.ApprovalIDs); err != nil {
		return err
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
	if err := execDeleteIDs(ctx, tx, "message", graph.MessageIDs); err != nil {
		return err
	}
	if err := execDeleteIDs(ctx, tx, "turn", graph.TurnIDs); err != nil {
		return err
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

func collectRunIDsForDelete(ctx context.Context, tx *sql.Tx, conversationIDs, turnIDs []string) ([]string, error) {
	values := map[string]struct{}{}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM run WHERE conversation_id IN (%s)", conversationIDs); err != nil {
		return nil, err
	}
	if err := addStringsFromQuery(ctx, tx, values, "SELECT id FROM run WHERE turn_id IN (%s)", turnIDs); err != nil {
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

func collectScheduleRunIDsForDelete(ctx context.Context, tx *sql.Tx, rows map[string]*conversationTreeRow, conversationIDs []string) ([]string, error) {
	values := map[string]struct{}{}
	for _, row := range rows {
		if row == nil {
			continue
		}
		if id := strings.TrimSpace(row.ScheduleRunID); id != "" {
			values[id] = struct{}{}
		}
	}
	if err := addStringsFromQueryOptional(ctx, tx, values, "SELECT id FROM schedule_run WHERE conversation_id IN (%s)", conversationIDs); err != nil {
		return nil, err
	}
	return sortedKeys(values), nil
}

func collectPayloadIDsForDelete(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) ([]string, error) {
	values := map[string]struct{}{}
	payloadQueries := []struct {
		query string
		ids   []string
	}{
		{query: "SELECT attachment_payload_id FROM message WHERE conversation_id IN (%s)", ids: graph.ConversationIDs},
		{query: "SELECT elicitation_payload_id FROM message WHERE conversation_id IN (%s)", ids: graph.ConversationIDs},
		{query: "SELECT request_payload_id FROM model_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT response_payload_id FROM model_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT provider_request_payload_id FROM model_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT provider_response_payload_id FROM model_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT stream_payload_id FROM model_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT request_payload_id FROM tool_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT response_payload_id FROM tool_call WHERE message_id IN (%s)", ids: graph.MessageIDs},
		{query: "SELECT payload_id FROM generated_file WHERE conversation_id IN (%s)", ids: graph.ConversationIDs},
	}
	for _, item := range payloadQueries {
		if err := addStringsFromQuery(ctx, tx, values, item.query, item.ids); err != nil {
			return nil, err
		}
	}
	return sortedKeys(values), nil
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

func addStringsFromQueryOptional(ctx context.Context, tx *sql.Tx, target map[string]struct{}, queryTemplate string, ids []string) error {
	rows, err := queryStringsForColumn(ctx, tx, queryTemplate, ids)
	if isOptionalTableMissing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, row := range rows {
		target[row] = struct{}{}
	}
	return nil
}

func queryStringsForColumn(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) ([]string, error) {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	values := map[string]struct{}{}
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var value sql.NullString
			if err := rows.Scan(&value); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if value.Valid {
				if normalized := strings.TrimSpace(value.String); normalized != "" {
					values[normalized] = struct{}{}
				}
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return sortedKeys(values), nil
}

func execDeleteIDs(ctx context.Context, tx *sql.Tx, table string, ids []string) error {
	return execIDs(ctx, tx, fmt.Sprintf("DELETE FROM %s WHERE id IN (%%s)", table), ids)
}

func optionalExecIDs(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) error {
	err := execIDs(ctx, tx, queryTemplate, ids)
	if isOptionalTableMissing(err) {
		return nil
	}
	return err
}

func execIDs(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string) error {
	ids = normalizeDeleteIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...); err != nil {
			return err
		}
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
       OR elicitation_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM model_call
    WHERE request_payload_id = call_payload.id
       OR response_payload_id = call_payload.id
       OR provider_request_payload_id = call_payload.id
       OR provider_response_payload_id = call_payload.id
       OR stream_payload_id = call_payload.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM tool_call
    WHERE request_payload_id = call_payload.id
       OR response_payload_id = call_payload.id
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
	return statusSet("running", "in_progress", "processing", "queued", "pending", "thinking", "streaming", "waiting_for_user", "prechecking", "executing")
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

func isOptionalTableMissing(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "unknown table")
}
