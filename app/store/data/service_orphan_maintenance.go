package data

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

type orphanMaintenanceRule struct {
	ID             string
	Action         OrphanMaintenanceAction
	Priority       int
	Table          string
	Alias          string
	RecordExpr     string
	KeyColumns     []string
	DetachColumn   string
	ReferenceTable string
	ReferenceExpr  string
	AgeExpr        string
	Predicate      string
	RequiredTables []string
}

// ListOrphanMaintenanceCandidates evaluates a static schema contract. It does
// not inspect information_schema, sqlite_master, or database metadata.
func (s *datlyService) ListOrphanMaintenanceCandidates(ctx context.Context, request OrphanMaintenanceCandidateRequest) ([]OrphanMaintenanceCandidate, error) {
	request.OlderThan = request.OlderThan.UTC()
	request.AfterCursor = strings.TrimSpace(request.AfterCursor)
	if request.OlderThan.IsZero() {
		return nil, fmt.Errorf("%w: orphan grace-period cutoff is required", ErrInvalidConversationMaintenanceRequest)
	}
	if request.Limit <= 0 {
		return nil, fmt.Errorf("%w: orphan candidate limit must be positive", ErrInvalidConversationMaintenanceRequest)
	}

	db, driver, err := s.dbWithDriver()
	if err != nil {
		return nil, err
	}
	capabilities, err := deleteSchemaCapabilitiesForDriver(driver)
	if err != nil {
		return nil, err
	}
	rules := orphanMaintenanceRules(capabilities)
	afterPriority, afterRule, afterRecord, err := decodeOrphanMaintenanceCursor(request.AfterCursor, rules)
	if err != nil {
		return nil, err
	}

	result := make([]OrphanMaintenanceCandidate, 0, request.Limit)
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if request.AfterCursor != "" && (rule.Priority < afterPriority || (rule.Priority == afterPriority && rule.ID < afterRule)) {
			continue
		}
		ruleAfterRecord := ""
		if request.AfterCursor != "" && rule.Priority == afterPriority && rule.ID == afterRule {
			ruleAfterRecord = afterRecord
		}
		page, listErr := listOrphanMaintenanceRuleCandidates(ctx, db, capabilities.driver, rule, request.OlderThan, ruleAfterRecord, request.Limit-len(result))
		if listErr != nil {
			return nil, listErr
		}
		result = append(result, page...)
		if len(result) == request.Limit {
			break
		}
	}
	return result, nil
}

func listOrphanMaintenanceRuleCandidates(ctx context.Context, db *sql.DB, driver string, rule orphanMaintenanceRule, olderThan time.Time, afterRecord string, limit int) ([]OrphanMaintenanceCandidate, error) {
	if limit <= 0 {
		return nil, nil
	}
	castType := "CHAR"
	recordAfterPredicate := "BINARY CAST(" + rule.RecordExpr + " AS CHAR) > BINARY ?"
	recordOrder := "BINARY CAST(" + rule.RecordExpr + " AS CHAR)"
	if strings.Contains(driver, "sqlite") {
		castType = "TEXT"
		recordAfterPredicate = "CAST(" + rule.RecordExpr + " AS TEXT) COLLATE BINARY > ? COLLATE BINARY"
		recordOrder = "CAST(" + rule.RecordExpr + " AS TEXT) COLLATE BINARY"
	}

	args := []interface{}{olderThan}
	cursorPredicate := ""
	if afterRecord != "" {
		cursorPredicate = "\n  AND " + recordAfterPredicate
		args = append(args, afterRecord)
	}
	args = append(args, limit)
	query := fmt.Sprintf(`SELECT CAST(%s AS %s) AS record_id,
       CAST(%s AS %s) AS reference_id,
       CAST(%s AS %s) AS observed_at
FROM %s %s
WHERE (%s)
  AND %s IS NOT NULL
  AND %s <= ?%s
ORDER BY %s ASC
LIMIT ?`, rule.RecordExpr, castType, rule.ReferenceExpr, castType, rule.AgeExpr, castType,
		rule.Table, rule.Alias, rule.Predicate, rule.AgeExpr, rule.AgeExpr, cursorPredicate, recordOrder)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list orphan candidates for rule %q: %w", rule.ID, err)
	}
	defer rows.Close()

	result := make([]OrphanMaintenanceCandidate, 0, limit)
	for rows.Next() {
		candidate := OrphanMaintenanceCandidate{
			RuleID: rule.ID, Action: rule.Action, Table: rule.Table, ReferenceTable: rule.ReferenceTable,
		}
		var rawReference sql.NullString
		var rawObserved sql.NullString
		if err = rows.Scan(&candidate.RecordID, &rawReference, &rawObserved); err != nil {
			return nil, fmt.Errorf("scan orphan candidate for rule %q: %w", rule.ID, err)
		}
		candidate.ReferenceID = rawReference.String
		switch candidate.Action {
		case OrphanMaintenanceSafeDelete, OrphanMaintenanceSafeDetach, OrphanMaintenanceReportOnly:
		default:
			return nil, fmt.Errorf("orphan candidate rule=%q has invalid action %q", candidate.RuleID, candidate.Action)
		}
		observedAt, ok := parseDBTime(rawObserved.String)
		if !rawObserved.Valid || !ok {
			return nil, fmt.Errorf("orphan candidate rule=%q record=%q has invalid observation time %q", candidate.RuleID, candidate.RecordID, rawObserved.String)
		}
		candidate.ObservedAt = observedAt
		candidate.CursorID = encodeOrphanMaintenanceCursor(rule.Priority, candidate.RuleID, candidate.RecordID)
		result = append(result, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orphan candidates for rule %q: %w", rule.ID, err)
	}
	return result, nil
}

func encodeOrphanMaintenanceCursor(priority int, ruleID, recordID string) string {
	return fmt.Sprintf("%06d%s%s%s%s", priority, orphanMaintenanceCursorOrderSeparator, ruleID, orphanMaintenanceCursorSeparator, recordID)
}

func decodeOrphanMaintenanceCursor(cursor string, rules []orphanMaintenanceRule) (int, string, string, error) {
	if cursor == "" {
		return 0, "", "", nil
	}
	parts := strings.SplitN(cursor, orphanMaintenanceCursorSeparator, 2)
	orderAndRule := strings.SplitN(parts[0], orphanMaintenanceCursorOrderSeparator, 2)
	if len(parts) != 2 || len(orderAndRule) != 2 || strings.TrimSpace(orderAndRule[0]) == "" || strings.TrimSpace(orderAndRule[1]) == "" || strings.TrimSpace(parts[1]) == "" {
		return 0, "", "", fmt.Errorf("%w: invalid orphan candidate cursor", ErrInvalidConversationMaintenanceRequest)
	}
	priority, err := strconv.Atoi(orderAndRule[0])
	if err != nil || priority <= 0 {
		return 0, "", "", fmt.Errorf("%w: invalid orphan candidate cursor", ErrInvalidConversationMaintenanceRequest)
	}
	for _, rule := range rules {
		if rule.ID == orderAndRule[1] {
			if rule.Priority != priority {
				return 0, "", "", fmt.Errorf("%w: orphan cursor priority does not match rule %q", ErrInvalidConversationMaintenanceRequest, rule.ID)
			}
			return priority, rule.ID, parts[1], nil
		}
	}
	return 0, "", "", fmt.Errorf("%w: orphan cursor references unknown rule %q", ErrInvalidConversationMaintenanceRequest, orderAndRule[1])
}

func orphanMaintenanceRules(capabilities *deleteSchemaCapabilities) []orphanMaintenanceRule {
	composite := func(parts ...string) string {
		if strings.Contains(capabilities.driver, "mysql") {
			casted := make([]string, 0, len(parts))
			for _, part := range parts {
				casted = append(casted, "COALESCE(CAST("+part+" AS CHAR), '')")
			}
			return "CONCAT(" + strings.Join(casted, ", CHAR(31), ") + ")"
		}
		casted := make([]string, 0, len(parts))
		for _, part := range parts {
			casted = append(casted, "COALESCE(CAST("+part+" AS TEXT), '')")
		}
		return strings.Join(casted, " || CHAR(31) || ")
	}
	required := func(id, table, alias, record, referenceTable, reference, parentID, age string, requiredTables ...string) orphanMaintenanceRule {
		return orphanMaintenanceRule{
			ID: id, Action: OrphanMaintenanceSafeDelete, Table: table, Alias: alias,
			RecordExpr: record, KeyColumns: []string{strings.TrimPrefix(record, alias+".")}, ReferenceTable: referenceTable, ReferenceExpr: reference, AgeExpr: age,
			Predicate:      fmt.Sprintf("(TRIM(COALESCE(%s, '')) = '' OR NOT EXISTS (SELECT 1 FROM %s orphan_parent WHERE orphan_parent.%s = %s))", reference, referenceTable, parentID, reference),
			RequiredTables: append([]string{table, referenceTable}, requiredTables...),
		}
	}
	optional := func(id, table, alias, record, referenceTable, reference, parentID, age string, requiredTables ...string) orphanMaintenanceRule {
		return orphanMaintenanceRule{
			ID: id, Action: OrphanMaintenanceSafeDetach, Table: table, Alias: alias,
			RecordExpr: record, KeyColumns: []string{strings.TrimPrefix(record, alias+".")}, DetachColumn: strings.TrimPrefix(reference, alias+"."), ReferenceTable: referenceTable, ReferenceExpr: reference, AgeExpr: age,
			Predicate:      fmt.Sprintf("TRIM(COALESCE(%s, '')) <> '' AND NOT EXISTS (SELECT 1 FROM %s orphan_parent WHERE orphan_parent.%s = %s)", reference, referenceTable, parentID, reference),
			RequiredTables: append([]string{table, referenceTable}, requiredTables...),
		}
	}

	conversationAge := "COALESCE(c.last_activity, c.updated_at, c.created_at)"
	goalAge := "COALESCE(g.updated_at, g.created_at)"
	turnAge := "t.created_at"
	queueAge := "COALESCE(tq.updated_at, tq.created_at)"
	messageAge := "COALESCE(m.updated_at, m.created_at)"
	modelAge := "COALESCE(mc.completed_at, mc.started_at)"
	toolAge := "COALESCE(tc.completed_at, tc.started_at)"
	approvalAge := "COALESCE(taq.updated_at, taq.created_at)"
	runAge := "COALESCE(r.completed_at, r.updated_at, r.created_at)"
	scheduleAge := "COALESCE(s.updated_at, s.created_at)"
	generatedAge := "COALESCE(gf.updated_at, gf.created_at)"

	rules := []orphanMaintenanceRule{
		required("goal.missing_conversation", "goal", "g", "g.id", "conversation", "g.conversation_id", "id", goalAge),
		required("turn.missing_conversation", "turn", "t", "t.id", "conversation", "t.conversation_id", "id", turnAge),
		required("turn_queue.missing_conversation", "turn_queue", "tq", "tq.id", "conversation", "tq.conversation_id", "id", queueAge),
		required("turn_queue.missing_turn", "turn_queue", "tq", "tq.id", "turn", "tq.turn_id", "id", queueAge),
		required("turn_queue.missing_message", "turn_queue", "tq", "tq.id", "message", "tq.message_id", "id", queueAge),
		required("message.missing_conversation", "message", "m", "m.id", "conversation", "m.conversation_id", "id", messageAge),
		required("model_call.missing_message", "model_call", "mc", "mc.message_id", "message", "mc.message_id", "id", modelAge),
		required("tool_call.missing_message", "tool_call", "tc", "tc.message_id", "message", "tc.message_id", "id", toolAge),
		required("generated_file.missing_conversation", "generated_file", "gf", "gf.id", "conversation", "gf.conversation_id", "id", generatedAge),
		required("tool_execution_claim.missing_turn", "tool_execution_claim", "tec", "tec.claim_key", "turn", "tec.turn_id", "id", "COALESCE(tec.updated_at, tec.created_at)"),
		required("report_export_artifact.missing_job", "report_export_artifact", "rea", "rea.artifact_id", "report_export_job", "rea.job_id", "job_id", "rea.created_at"),
		{
			ID: "conversation_report_context.missing_conversation", Action: OrphanMaintenanceSafeDelete,
			Table: "conversation_report_context", Alias: "crc", RecordExpr: composite("crc.owner_id", "crc.conversation_id"),
			KeyColumns:     []string{"owner_id", "conversation_id"},
			ReferenceTable: "conversation", ReferenceExpr: "crc.conversation_id", AgeExpr: "crc.updated_at",
			Predicate:      "TRIM(COALESCE(crc.conversation_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = crc.conversation_id)",
			RequiredTables: []string{"conversation_report_context", "conversation"},
		},
		{
			ID: "conversation_report_context.missing_active_report_run", Action: OrphanMaintenanceSafeDelete,
			Table: "conversation_report_context", Alias: "crc", RecordExpr: composite("crc.owner_id", "crc.conversation_id"),
			KeyColumns:     []string{"owner_id", "conversation_id"},
			ReferenceTable: "report_run", ReferenceExpr: composite("crc.owner_id", "crc.active_report_run_id"), AgeExpr: "crc.updated_at",
			Predicate:      "TRIM(COALESCE(crc.active_report_run_id, '')) = '' OR NOT EXISTS (SELECT 1 FROM report_run orphan_parent WHERE orphan_parent.owner_id = crc.owner_id AND orphan_parent.report_run_id = crc.active_report_run_id)",
			RequiredTables: []string{"conversation_report_context", "report_run"},
		},
		{
			ID: "call_payload.unused", Action: OrphanMaintenanceSafeDelete,
			Table: "call_payload", Alias: "cp", RecordExpr: "cp.id", ReferenceTable: "payload_consumers", ReferenceExpr: "cp.id", AgeExpr: "cp.created_at",
			Predicate: `NOT EXISTS (SELECT 1 FROM message m WHERE m.attachment_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM message m WHERE m.elicitation_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.request_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.response_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.provider_request_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.provider_response_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM model_call mc WHERE mc.stream_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM tool_call tc WHERE tc.request_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM tool_call tc WHERE tc.response_payload_id = cp.id)
AND NOT EXISTS (SELECT 1 FROM generated_file gf WHERE gf.payload_id = cp.id)`,
			RequiredTables: []string{"call_payload", "message", "model_call", "tool_call", "generated_file"},
		},

		optional("conversation.missing_parent_conversation", "conversation", "c", "c.id", "conversation", "c.conversation_parent_id", "id", conversationAge),
		optional("conversation.missing_parent_turn", "conversation", "c", "c.id", "turn", "c.conversation_parent_turn_id", "id", conversationAge),
		optional("conversation.missing_schedule", "conversation", "c", "c.id", "schedule", "c.schedule_id", "id", conversationAge),
		optional("conversation.missing_schedule_run", "conversation", "c", "c.id", "run", "c.schedule_run_id", "id", conversationAge),
		optional("message.missing_turn", "message", "m", "m.id", "turn", "m.turn_id", "id", messageAge),
		optional("message.missing_parent", "message", "m", "m.id", "message", "m.parent_message_id", "id", messageAge),
		optional("message.missing_superseded_by", "message", "m", "m.id", "message", "m.superseded_by", "id", messageAge),
		optional("message.missing_linked_conversation", "message", "m", "m.id", "conversation", "m.linked_conversation_id", "id", messageAge),
		optional("message.missing_attachment_payload", "message", "m", "m.id", "call_payload", "m.attachment_payload_id", "id", messageAge),
		optional("message.missing_elicitation_payload", "message", "m", "m.id", "call_payload", "m.elicitation_payload_id", "id", messageAge),
		optional("turn.missing_goal", "turn", "t", "t.id", "goal", "t.goal_id", "id", turnAge),
		optional("turn.missing_started_by_message", "turn", "t", "t.id", "message", "t.started_by_message_id", "id", turnAge),
		optional("turn.missing_retry_of", "turn", "t", "t.id", "turn", "t.retry_of", "id", turnAge),
		optional("turn.missing_run", "turn", "t", "t.id", "run", "t.run_id", "id", turnAge),
		optional("model_call.missing_turn", "model_call", "mc", "mc.message_id", "turn", "mc.turn_id", "id", modelAge),
		optional("model_call.missing_request_payload", "model_call", "mc", "mc.message_id", "call_payload", "mc.request_payload_id", "id", modelAge),
		optional("model_call.missing_response_payload", "model_call", "mc", "mc.message_id", "call_payload", "mc.response_payload_id", "id", modelAge),
		optional("model_call.missing_provider_request_payload", "model_call", "mc", "mc.message_id", "call_payload", "mc.provider_request_payload_id", "id", modelAge),
		optional("model_call.missing_provider_response_payload", "model_call", "mc", "mc.message_id", "call_payload", "mc.provider_response_payload_id", "id", modelAge),
		optional("model_call.missing_stream_payload", "model_call", "mc", "mc.message_id", "call_payload", "mc.stream_payload_id", "id", modelAge),
		optional("model_call.missing_run", "model_call", "mc", "mc.message_id", "run", "mc.run_id", "id", modelAge),
		optional("tool_call.missing_turn", "tool_call", "tc", "tc.message_id", "turn", "tc.turn_id", "id", toolAge),
		optional("tool_call.missing_request_payload", "tool_call", "tc", "tc.message_id", "call_payload", "tc.request_payload_id", "id", toolAge),
		optional("tool_call.missing_response_payload", "tool_call", "tc", "tc.message_id", "call_payload", "tc.response_payload_id", "id", toolAge),
		optional("tool_call.missing_run", "tool_call", "tc", "tc.message_id", "run", "tc.run_id", "id", toolAge),
		optional("tool_approval_queue.missing_conversation", "tool_approval_queue", "taq", "taq.id", "conversation", "taq.conversation_id", "id", approvalAge),
		optional("tool_approval_queue.missing_turn", "tool_approval_queue", "taq", "taq.id", "turn", "taq.turn_id", "id", approvalAge),
		optional("tool_approval_queue.missing_message", "tool_approval_queue", "taq", "taq.id", "message", "taq.message_id", "id", approvalAge),
		optional("run.missing_turn", "run", "r", "r.id", "turn", "r.turn_id", "id", runAge),
		optional("run.missing_schedule", "run", "r", "r.id", "schedule", "r.schedule_id", "id", runAge),
		optional("run.missing_conversation", "run", "r", "r.id", "conversation", "r.conversation_id", "id", runAge),
		optional("run.missing_resumed_from", "run", "r", "r.id", "run", "r.resumed_from_run_id", "id", runAge),
		optional("run.missing_checkpoint_message", "run", "r", "r.id", "message", "r.checkpoint_message_id", "id", runAge),
		optional("schedule.missing_conversation", "schedule", "s", "s.id", "conversation", "s.conversation_id", "id", scheduleAge),
		optional("schedule.missing_goal", "schedule", "s", "s.id", "goal", "s.goal_id", "id", scheduleAge),
		optional("generated_file.missing_turn", "generated_file", "gf", "gf.id", "turn", "gf.turn_id", "id", generatedAge),
		optional("generated_file.missing_message", "generated_file", "gf", "gf.id", "message", "gf.message_id", "id", generatedAge),
		optional("generated_file.missing_payload", "generated_file", "gf", "gf.id", "call_payload", "gf.payload_id", "id", generatedAge),
		optional("report_run.missing_conversation", "report_run", "rr", "rr.report_run_id", "conversation", "rr.conversation_id", "id", "COALESCE(rr.updated_at, rr.created_at)"),
		optional("report_export_job.missing_conversation", "report_export_job", "rej", "rej.job_id", "conversation", "rej.conversation_id", "id", "COALESCE(rej.completed_at, rej.started_at, rej.submitted_at)"),

		{
			ID: "report_export_job.missing_report_run", Action: OrphanMaintenanceReportOnly,
			Table: "report_export_job", Alias: "rej", RecordExpr: "rej.job_id", ReferenceTable: "report_run", ReferenceExpr: "rej.report_run_id",
			AgeExpr:        "COALESCE(rej.completed_at, rej.started_at, rej.submitted_at)",
			Predicate:      "TRIM(COALESCE(rej.report_run_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_run orphan_parent WHERE orphan_parent.report_run_id = rej.report_run_id AND orphan_parent.owner_id = rej.owner_id)",
			RequiredTables: []string{"report_export_job", "report_run"},
		},
		{
			ID: "report_export_job.missing_artifact", Action: OrphanMaintenanceReportOnly,
			Table: "report_export_job", Alias: "rej", RecordExpr: "rej.job_id", ReferenceTable: "report_export_artifact", ReferenceExpr: "rej.artifact_id",
			AgeExpr:        "COALESCE(rej.completed_at, rej.started_at, rej.submitted_at)",
			Predicate:      "TRIM(COALESCE(rej.artifact_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_export_artifact orphan_parent WHERE orphan_parent.artifact_id = rej.artifact_id)",
			RequiredTables: []string{"report_export_job", "report_export_artifact"},
		},
		// Disabled intentionally: job_id and artifact_id in report_audit_event are
		// optional event context, not ownership-defining foreign keys. Missing
		// targets are therefore not evidence that an audit event is an orphan.
		// Audit retention is handled separately by technical maintenance using
		// occurred_at. Do not restore these rules as delete actions.
		// {
		// 	ID: "report_audit_event.missing_job", Action: OrphanMaintenanceReportOnly,
		// 	Table: "report_audit_event", Alias: "rae", RecordExpr: "rae.event_id", ReferenceTable: "report_export_job", ReferenceExpr: "rae.job_id", AgeExpr: "rae.occurred_at",
		// 	Predicate:      "TRIM(COALESCE(rae.job_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_export_job orphan_parent WHERE orphan_parent.job_id = rae.job_id)",
		// 	RequiredTables: []string{"report_audit_event", "report_export_job"},
		// },
		// {
		// 	ID: "report_audit_event.missing_artifact", Action: OrphanMaintenanceReportOnly,
		// 	Table: "report_audit_event", Alias: "rae", RecordExpr: "rae.event_id", ReferenceTable: "report_export_artifact|report_shared_artifact", ReferenceExpr: "rae.artifact_id", AgeExpr: "rae.occurred_at",
		// 	Predicate: `TRIM(COALESCE(rae.artifact_id, '')) <> ''
		// AND NOT EXISTS (SELECT 1 FROM report_export_artifact export_artifact WHERE export_artifact.artifact_id = rae.artifact_id)
		// AND NOT EXISTS (SELECT 1 FROM report_shared_artifact shared_artifact WHERE shared_artifact.artifact_id = rae.artifact_id)`,
		// 	RequiredTables: []string{"report_audit_event", "report_export_artifact", "report_shared_artifact"},
		// },
		// Disabled intentionally: report_shared_artifact.source_artifact_id is a
		// logical source identity (for example report_<reportID>), not a foreign
		// key to report_shared_artifact.artifact_id. The historical rule below was
		// REPORT-ONLY. NEVER change it to a delete action: its predicate matches
		// valid saved reports and could cause permanent user-data loss.
		// {
		// 	ID: "report_shared_artifact.missing_source", Action: OrphanMaintenanceReportOnly,
		// 	Table: "report_shared_artifact", Alias: "rsa", RecordExpr: "rsa.artifact_id", ReferenceTable: "report_shared_artifact", ReferenceExpr: "rsa.source_artifact_id",
		// 	AgeExpr:        "COALESCE(rsa.updated_at, rsa.created_at)",
		// 	Predicate:      "TRIM(COALESCE(rsa.source_artifact_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM report_shared_artifact orphan_parent WHERE orphan_parent.artifact_id = rsa.source_artifact_id)",
		// 	RequiredTables: []string{"report_shared_artifact"},
		// },
	}

	if capabilities.hasTable("investigation") {
		rules = append(rules, orphanMaintenanceRule{
			ID: "investigation.missing_conversation", Action: OrphanMaintenanceSafeDetach,
			Table: "investigation", Alias: "i", RecordExpr: "i.id", KeyColumns: []string{"id"}, DetachColumn: "conversation_id",
			ReferenceTable: "conversation", ReferenceExpr: "i.conversation_id", AgeExpr: "i.created",
			Predicate:      "TRIM(COALESCE(i.conversation_id, '')) <> '' AND NOT EXISTS (SELECT 1 FROM conversation orphan_parent WHERE orphan_parent.id = i.conversation_id)",
			RequiredTables: []string{"investigation", "conversation"},
		})
	}
	if capabilities.hasTable("schedule_run") {
		rules = append(rules,
			required("schedule_run.missing_schedule", "schedule_run", "sr", "sr.id", "schedule", "sr.schedule_id", "id", "COALESCE(sr.completed_at, sr.updated_at, sr.created_at)"),
			optional("schedule_run.missing_conversation", "schedule_run", "sr", "sr.id", "conversation", "sr.conversation_id", "id", "COALESCE(sr.completed_at, sr.updated_at, sr.created_at)"),
		)
		for i := range rules {
			if rules[i].ID == "conversation.missing_schedule_run" {
				rules[i].ReferenceTable = "run|schedule_run"
				rules[i].Predicate = `TRIM(COALESCE(c.schedule_run_id, '')) <> ''
AND NOT EXISTS (SELECT 1 FROM run current_run WHERE current_run.id = c.schedule_run_id)
AND NOT EXISTS (SELECT 1 FROM schedule_run legacy_run WHERE legacy_run.id = c.schedule_run_id)`
				rules[i].RequiredTables = []string{"conversation", "run", "schedule_run"}
				break
			}
		}
	}

	filtered := rules[:0]
	for _, rule := range rules {
		available := true
		for _, table := range rule.RequiredTables {
			if !capabilities.hasTable(table) {
				available = false
				break
			}
		}
		if available {
			if len(rule.KeyColumns) == 0 {
				column := strings.TrimPrefix(rule.RecordExpr, rule.Alias+".")
				if isOrphanMaintenanceIdentifier(column) {
					rule.KeyColumns = []string{column}
				}
			}
			rule.Priority = orphanMaintenancePriority(rule.Action, rule.Table)
			filtered = append(filtered, rule)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Priority != filtered[j].Priority {
			return filtered[i].Priority < filtered[j].Priority
		}
		return filtered[i].ID < filtered[j].ID
	})
	return filtered
}

func orphanMaintenancePriority(action OrphanMaintenanceAction, table string) int {
	actionBase := 2000
	switch action {
	case OrphanMaintenanceSafeDetach:
		actionBase = 0
	case OrphanMaintenanceSafeDelete:
		actionBase = 1000
	}
	tableOrder := map[string]int{
		"tool_execution_claim":        10,
		"turn_queue":                  20,
		"tool_approval_queue":         30,
		"model_call":                  40,
		"tool_call":                   50,
		"generated_file":              60,
		"report_export_artifact":      70,
		"conversation_report_context": 80,
		"investigation":               90,
		"message":                     100,
		"run":                         110,
		"schedule_run":                120,
		"turn":                        130,
		"report_export_job":           140,
		"report_audit_event":          150,
		"report_run":                  160,
		"schedule":                    170,
		"goal":                        180,
		"conversation":                190,
		"report_shared_artifact":      200,
		"call_payload":                210,
	}
	priority, ok := tableOrder[table]
	if !ok {
		priority = 900
	}
	return actionBase + priority
}

func isOrphanMaintenanceIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func orphanRuleByID(rules []orphanMaintenanceRule, id string) *orphanMaintenanceRule {
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i]
		}
	}
	return nil
}
