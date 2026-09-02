package data

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/viant/agently-core/internal/auth"
)

type investigationDeletePolicy uint8

const (
	// Investigations are retained in stage 1. Only their database reference to a
	// deleted conversation is detached. The second value intentionally keeps the
	// deletion path ready for a later, explicit product decision.
	investigationRetainAndDetach investigationDeletePolicy = iota
	investigationDelete
)

const conversationInvestigationPolicy = investigationRetainAndDetach

var conversationDeleteSchemaTables = []string{
	"conversation",
	"goal",
	"turn",
	"turn_queue",
	"message",
	"model_call",
	"tool_call",
	"tool_approval_queue",
	"tool_execution_claim",
	"run",
	"schedule",
	"schedule_run",
	"generated_file",
	"call_payload",
	"investigation",
	"report_run",
	"conversation_report_context",
	"report_export_job",
	"report_export_artifact",
	"report_audit_event",
	"report_shared_artifact",
}

type deleteSchemaCapabilities struct {
	driver            string
	unavailableTables map[string]struct{}
}

func (c *deleteSchemaCapabilities) hasTable(table string) bool {
	if c == nil {
		return false
	}
	_, unavailable := c.unavailableTables[strings.ToLower(strings.TrimSpace(table))]
	return !unavailable
}

func (c *deleteSchemaCapabilities) hasColumn(table, column string) bool {
	return c != nil && strings.TrimSpace(column) != "" && c.hasTable(table)
}

// deleteSchemaCapabilitiesForDriver describes the current schema contract for
// each supported driver. It deliberately does not inspect the connected
// database: deployments are expected to provision the matching schema before
// the application starts.
func deleteSchemaCapabilitiesForDriver(driver string) (*deleteSchemaCapabilities, error) {
	driver = strings.ToLower(strings.TrimSpace(driver))
	switch {
	case strings.Contains(driver, "sqlite"):
		return &deleteSchemaCapabilities{
			driver: driver,
			unavailableTables: map[string]struct{}{
				"investigation": {},
				"schedule_run":  {},
			},
		}, nil
	case strings.Contains(driver, "mysql"):
		return &deleteSchemaCapabilities{driver: driver}, nil
	default:
		return nil, fmt.Errorf("unsupported deletion database driver %q", driver)
	}
}

func (s *datlyService) deleteConversationTreeDirect(ctx context.Context, rootIDs []string, now time.Time) error {
	userID := strings.TrimSpace(auth.EffectiveUserID(ctx))
	if userID == "" {
		return ErrPermissionDenied
	}
	phaseStarted := conversationDeleteDiagPhaseStart(ctx, "database_resolve")
	db, driver, err := s.dbWithDriver()
	conversationDeleteDiagPhaseDone(ctx, "database_resolve", phaseStarted, err, fmt.Sprintf("driver=%q", driver))
	if err != nil {
		return err
	}
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "transaction_begin")
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	conversationDeleteDiagPhaseDone(ctx, "transaction_begin", phaseStarted, err, fmt.Sprintf("driver=%q isolation=serializable", driver))
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackStarted := conversationDeleteDiagPhaseStart(ctx, "transaction_rollback")
			rollbackErr := tx.Rollback()
			conversationDeleteDiagPhaseDone(ctx, "transaction_rollback", rollbackStarted, rollbackErr, "")
		}
	}()

	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return err
	}
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "graph_build")
	graph, err := buildConversationDeleteGraph(ctx, tx, rootIDs, capabilities)
	conversationDeleteDiagPhaseDone(ctx, "graph_build", phaseStarted, err, conversationDeleteGraphDetails(graph))
	if err != nil {
		return err
	}
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "authorize")
	if err := authorizeConversationTreeDelete(graph.Rows, userID); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "authorize", phaseStarted, err, conversationDeleteGraphDetails(graph))
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "authorize", phaseStarted, nil, conversationDeleteGraphDetails(graph))
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "graph_lock")
	if err := lockConversationGraphForDelete(ctx, tx, graph); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "graph_lock", phaseStarted, err, conversationDeleteGraphDetails(graph))
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "graph_lock", phaseStarted, nil, conversationDeleteGraphDetails(graph))
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "graph_prepare")
	if err := prepareConversationDeleteGraph(ctx, tx, graph, userID, now.UTC()); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "graph_prepare", phaseStarted, err, conversationDeleteGraphDetails(graph))
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "graph_prepare", phaseStarted, nil, conversationDeleteGraphDetails(graph))
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "graph_validate")
	if err := validateConversationDeleteGraph(ctx, tx, graph, now.UTC()); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "graph_validate", phaseStarted, err, conversationDeleteGraphDetails(graph))
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "graph_validate", phaseStarted, nil, conversationDeleteGraphDetails(graph))
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "graph_delete")
	if err := deleteConversationGraph(ctx, tx, graph); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "graph_delete", phaseStarted, err, conversationDeleteGraphDetails(graph))
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "graph_delete", phaseStarted, nil, conversationDeleteGraphDetails(graph))
	phaseStarted = conversationDeleteDiagPhaseStart(ctx, "transaction_commit")
	if err := tx.Commit(); err != nil {
		conversationDeleteDiagPhaseDone(ctx, "transaction_commit", phaseStarted, err, "")
		return err
	}
	conversationDeleteDiagPhaseDone(ctx, "transaction_commit", phaseStarted, nil, "")
	committed = true
	return nil
}

func lockConversationGraphForDelete(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	if graph == nil || graph.Capabilities == nil || !strings.Contains(graph.Capabilities.driver, "mysql") {
		return nil
	}
	for _, chunk := range chunkStrings(graph.ConversationIDs, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT id FROM conversation WHERE id IN (%s) FOR UPDATE", placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func prepareConversationDeleteGraph(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, userID string, now time.Time) error {
	var err error
	if graph.Capabilities.hasColumn("goal", "conversation_id") {
		graph.GoalIDs, err = queryStringsForColumn(ctx, tx, "SELECT id FROM goal WHERE conversation_id IN (%s)", graph.ConversationIDs)
		if err != nil {
			return err
		}
	}
	if err := ensureNoInboundConversationReferences(ctx, tx, graph); err != nil {
		return err
	}
	graph.ScheduleIDs, err = collectGoalWakeupSchedules(ctx, tx, graph, userID, now)
	if err != nil {
		return err
	}
	if graph.Capabilities.hasColumn("run", "schedule_id") {
		runIDs := makeStringSet(graph.RunIDs)
		if err := addStringsFromQuery(ctx, tx, runIDs, "SELECT id FROM run WHERE schedule_id IN (%s)", graph.ScheduleIDs); err != nil {
			return err
		}
		graph.RunIDs = sortedKeys(runIDs)
	}
	if graph.Capabilities.hasColumn("schedule_run", "schedule_id") {
		scheduleRunIDs := makeStringSet(graph.ScheduleRunIDs)
		if err := addStringsFromQuery(ctx, tx, scheduleRunIDs, "SELECT id FROM schedule_run WHERE schedule_id IN (%s)", graph.ScheduleIDs); err != nil {
			return err
		}
		graph.ScheduleRunIDs = sortedKeys(scheduleRunIDs)
	}
	if err := prepareReportDeleteGraph(ctx, tx, graph, userID); err != nil {
		return err
	}
	return nil
}

func validateConversationDeleteGraph(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, now time.Time) error {
	if err := refreshConversationDeleteRunIDs(ctx, tx, graph); err != nil {
		return err
	}
	if err := ensureConversationGraphDeletableOrEmpty(ctx, tx, graph); err != nil {
		return err
	}
	if err := ensureNoLiveConversationRuns(ctx, tx, graph, now); err != nil {
		return err
	}
	return ensureNoActiveReportExports(ctx, tx, graph)
}

func refreshConversationDeleteRunIDs(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	if graph == nil {
		return nil
	}
	runIDs, err := collectRunIDsForDelete(ctx, tx, graph.ConversationIDs, graph.TurnIDs)
	if err != nil {
		return err
	}
	allRunIDs := makeStringSet(graph.RunIDs)
	for _, runID := range runIDs {
		allRunIDs[runID] = struct{}{}
	}
	graph.RunIDs = sortedKeys(allRunIDs)
	return nil
}

func ensureNoInboundConversationReferences(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	inside := makeStringSet(graph.ConversationIDs)
	sources, err := queryStringsForColumn(ctx, tx, "SELECT conversation_id FROM message WHERE linked_conversation_id IN (%s)", graph.ConversationIDs)
	if err != nil {
		return err
	}
	for _, sourceID := range sources {
		if _, ok := inside[sourceID]; !ok {
			return ErrConversationGraphReferenced
		}
	}
	if graph.Capabilities.hasColumn("conversation", "conversation_parent_turn_id") {
		children, err := queryStringsForColumn(ctx, tx, "SELECT id FROM conversation WHERE conversation_parent_turn_id IN (%s)", graph.TurnIDs)
		if err != nil {
			return err
		}
		for _, childID := range children {
			if _, ok := inside[childID]; !ok {
				return ErrConversationGraphReferenced
			}
		}
	}
	if graph.Capabilities.hasColumn("turn", "goal_id") {
		conversationIDs, err := queryStringsForColumn(ctx, tx, "SELECT conversation_id FROM turn WHERE goal_id IN (%s)", graph.GoalIDs)
		if err != nil {
			return err
		}
		for _, conversationID := range conversationIDs {
			if _, ok := inside[conversationID]; !ok {
				return ErrConversationGraphReferenced
			}
		}
	}
	return nil
}

type deleteScheduleDependency struct {
	ID             string
	Name           string
	OwnerID        string
	Internal       bool
	ConversationID string
	GoalID         string
	ScheduleType   string
	LeaseUntil     string
}

func collectGoalWakeupSchedules(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, userID string, now time.Time) ([]string, error) {
	capabilities := graph.Capabilities
	if !capabilities.hasTable("schedule") || (!capabilities.hasColumn("schedule", "conversation_id") && !capabilities.hasColumn("schedule", "goal_id")) {
		return nil, nil
	}
	for _, column := range []string{"id", "name", "created_by_user_id", "internal", "conversation_id", "goal_id", "schedule_type", "lease_until"} {
		if !capabilities.hasColumn("schedule", column) {
			return nil, fmt.Errorf("conversation deletion schema is missing schedule.%s", column)
		}
	}
	dependencies := map[string]*deleteScheduleDependency{}
	if err := addDeleteScheduleDependencies(ctx, tx, dependencies, "conversation_id", graph.ConversationIDs); err != nil {
		return nil, err
	}
	if err := addDeleteScheduleDependencies(ctx, tx, dependencies, "goal_id", graph.GoalIDs); err != nil {
		return nil, err
	}
	conversationSet := makeStringSet(graph.ConversationIDs)
	goalSet := makeStringSet(graph.GoalIDs)
	result := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if !dependency.Internal {
			return nil, fmt.Errorf("%w: schedule=%s", ErrConversationScheduleReferenced, dependency.ID)
		}
		if dependency.OwnerID != "" && dependency.OwnerID != userID {
			return nil, ErrPermissionDenied
		}
		if !isGoalWakeupDependency(dependency, conversationSet, goalSet) {
			return nil, fmt.Errorf("%w: internal schedule=%s is not an owned goal wakeup", ErrConversationScheduleReferenced, dependency.ID)
		}
		if leaseUntil, ok := parseDBTime(dependency.LeaseUntil); ok && leaseUntil.After(now) {
			return nil, ErrConversationActive
		}
		result = append(result, dependency.ID)
	}
	return normalizeDeleteIDs(result), nil
}

func addDeleteScheduleDependencies(ctx context.Context, tx *sql.Tx, target map[string]*deleteScheduleDependency, column string, ids []string) error {
	ids = normalizeDeleteIDs(ids)
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		query := fmt.Sprintf(`SELECT id, COALESCE(name, ''), COALESCE(created_by_user_id, ''), COALESCE(internal, 0),
       COALESCE(conversation_id, ''), COALESCE(goal_id, ''), COALESCE(schedule_type, ''), CAST(lease_until AS CHAR)
FROM schedule WHERE %s IN (%s)`, column, placeholders(len(chunk)))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			dependency := &deleteScheduleDependency{}
			var internal int
			var leaseUntil sql.NullString
			if err := rows.Scan(&dependency.ID, &dependency.Name, &dependency.OwnerID, &internal, &dependency.ConversationID, &dependency.GoalID, &dependency.ScheduleType, &leaseUntil); err != nil {
				_ = rows.Close()
				return err
			}
			dependency.Internal = internal != 0
			dependency.ID = strings.TrimSpace(dependency.ID)
			dependency.Name = strings.TrimSpace(dependency.Name)
			dependency.OwnerID = strings.TrimSpace(dependency.OwnerID)
			dependency.ConversationID = strings.TrimSpace(dependency.ConversationID)
			dependency.GoalID = strings.TrimSpace(dependency.GoalID)
			dependency.ScheduleType = normalizeStatus(dependency.ScheduleType)
			dependency.LeaseUntil = leaseUntil.String
			target[dependency.ID] = dependency
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func isGoalWakeupDependency(dependency *deleteScheduleDependency, conversationIDs, goalIDs map[string]struct{}) bool {
	if dependency == nil || dependency.ConversationID == "" || dependency.GoalID == "" || dependency.ScheduleType != "adhoc" {
		return false
	}
	if _, ok := conversationIDs[dependency.ConversationID]; !ok {
		return false
	}
	if _, ok := goalIDs[dependency.GoalID]; !ok {
		return false
	}
	return dependency.ID == "goal-wakeup-"+dependency.GoalID && dependency.Name == "autonomous::goal-wakeup::"+dependency.GoalID
}

func prepareReportDeleteGraph(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, userID string) error {
	capabilities := graph.Capabilities
	if capabilities.hasColumn("conversation_report_context", "conversation_id") && capabilities.hasColumn("conversation_report_context", "owner_id") {
		if err := validateOwnedRows(ctx, tx, "SELECT owner_id FROM conversation_report_context WHERE conversation_id IN (%s)", graph.ConversationIDs, userID); err != nil {
			return err
		}
	}
	runIDs := map[string]struct{}{}
	if capabilities.hasColumn("report_run", "conversation_id") {
		if err := addStringsFromQuery(ctx, tx, runIDs, "SELECT report_run_id FROM report_run WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("conversation_report_context", "conversation_id") && capabilities.hasColumn("conversation_report_context", "active_report_run_id") {
		if err := addStringsFromQuery(ctx, tx, runIDs, "SELECT active_report_run_id FROM conversation_report_context WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
			return err
		}
	}
	graph.ReportRunIDs = sortedKeys(runIDs)
	if capabilities.hasTable("report_run") {
		if err := validateOwnedReportRuns(ctx, tx, graph.ReportRunIDs, userID); err != nil {
			return err
		}
		if err := ensureNoExternalReportContexts(ctx, tx, graph); err != nil {
			return err
		}
	}
	jobIDs := map[string]struct{}{}
	if capabilities.hasColumn("report_export_job", "conversation_id") {
		if err := addStringsFromQuery(ctx, tx, jobIDs, "SELECT job_id FROM report_export_job WHERE conversation_id IN (%s)", graph.ConversationIDs); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("report_export_job", "report_run_id") {
		if err := addStringsFromQuery(ctx, tx, jobIDs, "SELECT job_id FROM report_export_job WHERE report_run_id IN (%s)", graph.ReportRunIDs); err != nil {
			return err
		}
	}
	graph.ReportJobIDs = sortedKeys(jobIDs)
	if capabilities.hasTable("report_export_job") {
		if err := validateOwnedReportJobs(ctx, tx, graph.ReportJobIDs, userID); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("report_export_artifact", "job_id") && capabilities.hasColumn("report_export_artifact", "owner_id") {
		if err := validateOwnedRows(ctx, tx, "SELECT owner_id FROM report_export_artifact WHERE job_id IN (%s)", graph.ReportJobIDs, userID); err != nil {
			return err
		}
	}
	return nil
}

func validateOwnedReportRuns(ctx context.Context, tx *sql.Tx, ids []string, userID string) error {
	return validateOwnedRows(ctx, tx, "SELECT owner_id FROM report_run WHERE report_run_id IN (%s)", ids, userID)
}

func validateOwnedReportJobs(ctx context.Context, tx *sql.Tx, ids []string, userID string) error {
	return validateOwnedRows(ctx, tx, "SELECT owner_id FROM report_export_job WHERE job_id IN (%s)", ids, userID)
}

func validateOwnedRows(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string, userID string) error {
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ownerID sql.NullString
			if err := rows.Scan(&ownerID); err != nil {
				_ = rows.Close()
				return err
			}
			if strings.TrimSpace(ownerID.String) != userID {
				_ = rows.Close()
				return ErrPermissionDenied
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func ensureNoExternalReportContexts(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	if !graph.Capabilities.hasColumn("conversation_report_context", "active_report_run_id") {
		return nil
	}
	inside := makeStringSet(graph.ConversationIDs)
	conversationIDs, err := queryStringsForColumn(ctx, tx, "SELECT conversation_id FROM conversation_report_context WHERE active_report_run_id IN (%s)", graph.ReportRunIDs)
	if err != nil {
		return err
	}
	for _, conversationID := range conversationIDs {
		if _, ok := inside[conversationID]; !ok {
			return ErrConversationGraphReferenced
		}
	}
	return nil
}

func ensureConversationGraphDeletableOrEmpty(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	activity := map[string]struct{}{}
	queries := []struct {
		table  string
		column string
		query  string
	}{
		{table: "turn", column: "conversation_id", query: "SELECT conversation_id FROM turn WHERE conversation_id IN (%s)"},
		{table: "message", column: "conversation_id", query: "SELECT conversation_id FROM message WHERE conversation_id IN (%s)"},
		{table: "turn_queue", column: "conversation_id", query: "SELECT conversation_id FROM turn_queue WHERE conversation_id IN (%s)"},
		{table: "run", column: "conversation_id", query: "SELECT conversation_id FROM run WHERE conversation_id IN (%s)"},
		{table: "goal", column: "conversation_id", query: "SELECT conversation_id FROM goal WHERE conversation_id IN (%s)"},
		{table: "schedule", column: "conversation_id", query: "SELECT conversation_id FROM schedule WHERE conversation_id IN (%s)"},
		{table: "schedule_run", column: "conversation_id", query: "SELECT conversation_id FROM schedule_run WHERE conversation_id IN (%s)"},
		{table: "report_run", column: "conversation_id", query: "SELECT conversation_id FROM report_run WHERE conversation_id IN (%s)"},
		{table: "report_export_job", column: "conversation_id", query: "SELECT conversation_id FROM report_export_job WHERE conversation_id IN (%s)"},
		{table: "conversation_report_context", column: "conversation_id", query: "SELECT conversation_id FROM conversation_report_context WHERE conversation_id IN (%s)"},
	}
	for _, item := range queries {
		if !graph.Capabilities.hasColumn(item.table, item.column) {
			continue
		}
		if err := addStringsFromQuery(ctx, tx, activity, item.query, graph.ConversationIDs); err != nil {
			return err
		}
	}
	terminal := statusSet("succeeded", "completed", "complete", "success", "done", "ok", "failed", "error", "canceled", "cancelled", "terminated", "compacted", "pruned")
	deletableNonTerminal := conversationDeleteActiveStatuses()
	for _, row := range graph.Rows {
		if row == nil {
			continue
		}
		status := normalizeStatus(row.Status)
		if _, ok := terminal[status]; ok {
			continue
		}
		// Legacy conversations may predate status persistence and therefore have
		// a NULL or empty status. Let them reach the run liveness check instead of
		// treating the missing metadata itself as proof of active execution.
		if status == "" {
			continue
		}
		// Known lifecycle states are allowed to reach the run liveness check.
		// This covers resumable and stale active-looking conversations without
		// treating unknown legacy states as safe to delete.
		if _, ok := deletableNonTerminal[status]; ok {
			continue
		}
		if _, empty := activity[row.ID]; empty {
			return fmt.Errorf("%w: conversation=%s status=%s", ErrConversationNonTerminal, row.ID, strings.TrimSpace(row.Status))
		}
	}
	return nil
}

type conversationRunDeleteDecision struct {
	BlocksDelete     bool
	Reason           string
	LeasePresent     bool
	LeaseValid       bool
	LeaseCurrent     bool
	HeartbeatPresent bool
	HeartbeatValid   bool
	HeartbeatFresh   bool
	Grace            time.Duration
}

func evaluateConversationRunDeleteDecision(status string, leaseUntil, heartbeat sql.NullString, heartbeatIntervalSeconds int64, now time.Time) conversationRunDeleteDecision {
	decision := conversationRunDeleteDecision{
		Reason:           "status_not_active",
		LeasePresent:     leaseUntil.Valid && strings.TrimSpace(leaseUntil.String) != "",
		HeartbeatPresent: heartbeat.Valid && strings.TrimSpace(heartbeat.String) != "",
	}
	if _, active := runDeleteActiveStatuses()[normalizeStatus(status)]; !active {
		return decision
	}

	interval := time.Duration(heartbeatIntervalSeconds) * time.Second
	if interval < 0 {
		interval = 0
	}
	decision.Grace = 2 * interval
	if decision.Grace < 15*time.Second {
		decision.Grace = 15 * time.Second
	}

	if decision.LeasePresent {
		leaseAt, valid := parseDBTime(leaseUntil.String)
		decision.LeaseValid = valid
		if !valid {
			decision.BlocksDelete = true
			decision.Reason = "lease_invalid"
			return decision
		}
		decision.LeaseCurrent = leaseAt.After(now)
		if decision.LeaseCurrent {
			decision.BlocksDelete = true
			decision.Reason = "lease_current"
			return decision
		}
	}

	if decision.HeartbeatPresent {
		heartbeatAt, valid := parseDBTime(heartbeat.String)
		decision.HeartbeatValid = valid
		if valid {
			decision.HeartbeatFresh = !heartbeatAt.Before(now.Add(-decision.Grace))
			if decision.HeartbeatFresh {
				decision.BlocksDelete = true
				decision.Reason = "heartbeat_fresh"
				return decision
			}
		}
	}

	decision.Reason = "stale"
	return decision
}

func ensureNoLiveConversationRuns(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph, now time.Time) error {
	capabilities := graph.Capabilities
	leaseExpr := "NULL"
	heartbeatExpr := "NULL"
	intervalExpr := "5"
	if capabilities.hasColumn("run", "lease_until") {
		leaseExpr = "CAST(lease_until AS CHAR)"
	}
	if capabilities.hasColumn("run", "last_heartbeat_at") {
		heartbeatExpr = "CAST(last_heartbeat_at AS CHAR)"
	}
	if capabilities.hasColumn("run", "heartbeat_interval_sec") {
		intervalExpr = "COALESCE(heartbeat_interval_sec, 5)"
	}
	for _, chunk := range chunkStrings(graph.RunIDs, deleteChunkSize) {
		query := fmt.Sprintf("SELECT id, status, %s, %s, %s FROM run WHERE id IN (%s)", leaseExpr, heartbeatExpr, intervalExpr, placeholders(len(chunk)))
		rows, err := tx.QueryContext(ctx, query, stringArgs(chunk)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var runID string
			var status sql.NullString
			var leaseUntil, heartbeat sql.NullString
			var heartbeatInterval sql.NullInt64
			if err := rows.Scan(&runID, &status, &leaseUntil, &heartbeat, &heartbeatInterval); err != nil {
				_ = rows.Close()
				return err
			}
			decision := evaluateConversationRunDeleteDecision(status.String, leaseUntil, heartbeat, heartbeatInterval.Int64, now)
			conversationDeleteDiagRunDecision(ctx, runID, status.String, heartbeatInterval.Int64, decision)
			if decision.BlocksDelete {
				_ = rows.Close()
				return ErrConversationActive
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if capabilities.hasColumn("schedule_run", "lease_until") {
		for _, chunk := range chunkStrings(graph.ScheduleRunIDs, deleteChunkSize) {
			rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT status, CAST(lease_until AS CHAR) FROM schedule_run WHERE id IN (%s)", placeholders(len(chunk))), stringArgs(chunk)...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var status, leaseUntil sql.NullString
				if err := rows.Scan(&status, &leaseUntil); err != nil {
					_ = rows.Close()
					return err
				}
				if _, active := runDeleteActiveStatuses()[normalizeStatus(status.String)]; active && isNonExpiredDBTime(leaseUntil, now) {
					_ = rows.Close()
					return ErrConversationActive
				}
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func isNonExpiredDBTime(value sql.NullString, now time.Time) bool {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return false
	}
	parsed, ok := parseDBTime(value.String)
	return !ok || parsed.After(now)
}

func ensureNoActiveReportExports(ctx context.Context, tx *sql.Tx, graph *conversationDeleteGraph) error {
	if graph.Capabilities.hasTable("report_export_job") {
		if active, err := hasStatusInSet(ctx, tx, "SELECT status FROM report_export_job WHERE job_id IN (%s)", graph.ReportJobIDs, statusSet("queued", "running")); err != nil {
			return err
		} else if active {
			return ErrConversationActive
		}
	}
	return nil
}

func hasStatusInSet(ctx context.Context, tx *sql.Tx, queryTemplate string, ids []string, statuses map[string]struct{}) (bool, error) {
	for _, chunk := range chunkStrings(ids, deleteChunkSize) {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(queryTemplate, placeholders(len(chunk))), stringArgs(chunk)...)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var status sql.NullString
			if err := rows.Scan(&status); err != nil {
				_ = rows.Close()
				return false, err
			}
			if _, ok := statuses[normalizeStatus(status.String)]; ok {
				_ = rows.Close()
				return true, nil
			}
		}
		if err := rows.Close(); err != nil {
			return false, err
		}
	}
	return false, nil
}

func makeStringSet(ids []string) map[string]struct{} {
	result := make(map[string]struct{}, len(ids))
	for _, id := range normalizeDeleteIDs(ids) {
		result[id] = struct{}{}
	}
	return result
}
