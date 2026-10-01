package orphanmaintenance

import "sort"

type Action string

const (
	SafeDelete Action = "safe-delete"
	SafeDetach Action = "safe-detach"
	ReportOnly Action = "report-only"
)

type Rule struct {
	ID             string
	Action         Action
	Priority       int
	Table          string
	Keys           []string
	DetachColumn   string
	ReferenceTable string
	MySQLOnly      bool
}

var staticRules = []Rule{
	{ID: "tool_approval_queue.missing_conversation", Action: Action("safe-detach"), Priority: 30, Table: "tool_approval_queue", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id"},
	{ID: "tool_approval_queue.missing_message", Action: Action("safe-detach"), Priority: 30, Table: "tool_approval_queue", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "message_id"},
	{ID: "tool_approval_queue.missing_turn", Action: Action("safe-detach"), Priority: 30, Table: "tool_approval_queue", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "model_call.missing_provider_request_payload", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "provider_request_payload_id"},
	{ID: "model_call.missing_provider_response_payload", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "provider_response_payload_id"},
	{ID: "model_call.missing_request_payload", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "request_payload_id"},
	{ID: "model_call.missing_response_payload", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "response_payload_id"},
	{ID: "model_call.missing_run", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "run", DetachColumn: "run_id"},
	{ID: "model_call.missing_stream_payload", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "stream_payload_id"},
	{ID: "model_call.missing_turn", Action: Action("safe-detach"), Priority: 40, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "tool_call.missing_request_payload", Action: Action("safe-detach"), Priority: 50, Table: "tool_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "request_payload_id"},
	{ID: "tool_call.missing_response_payload", Action: Action("safe-detach"), Priority: 50, Table: "tool_call", Keys: []string{"message_id"}, ReferenceTable: "call_payload", DetachColumn: "response_payload_id"},
	{ID: "tool_call.missing_run", Action: Action("safe-detach"), Priority: 50, Table: "tool_call", Keys: []string{"message_id"}, ReferenceTable: "run", DetachColumn: "run_id"},
	{ID: "tool_call.missing_turn", Action: Action("safe-detach"), Priority: 50, Table: "tool_call", Keys: []string{"message_id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "generated_file.missing_message", Action: Action("safe-detach"), Priority: 60, Table: "generated_file", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "message_id"},
	{ID: "generated_file.missing_payload", Action: Action("safe-detach"), Priority: 60, Table: "generated_file", Keys: []string{"id"}, ReferenceTable: "call_payload", DetachColumn: "payload_id"},
	{ID: "generated_file.missing_turn", Action: Action("safe-detach"), Priority: 60, Table: "generated_file", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "message.missing_attachment_payload", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "call_payload", DetachColumn: "attachment_payload_id"},
	{ID: "message.missing_elicitation_payload", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "call_payload", DetachColumn: "elicitation_payload_id"},
	{ID: "message.missing_linked_conversation", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "linked_conversation_id"},
	{ID: "message.missing_parent", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "parent_message_id"},
	{ID: "message.missing_superseded_by", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "superseded_by"},
	{ID: "message.missing_turn", Action: Action("safe-detach"), Priority: 100, Table: "message", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "run.missing_checkpoint_message", Action: Action("safe-detach"), Priority: 110, Table: "run", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "checkpoint_message_id"},
	{ID: "run.missing_conversation", Action: Action("safe-detach"), Priority: 110, Table: "run", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id"},
	{ID: "run.missing_resumed_from", Action: Action("safe-detach"), Priority: 110, Table: "run", Keys: []string{"id"}, ReferenceTable: "run", DetachColumn: "resumed_from_run_id"},
	{ID: "run.missing_schedule", Action: Action("safe-detach"), Priority: 110, Table: "run", Keys: []string{"id"}, ReferenceTable: "schedule", DetachColumn: "schedule_id"},
	{ID: "run.missing_turn", Action: Action("safe-detach"), Priority: 110, Table: "run", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "turn_id"},
	{ID: "schedule_run.missing_conversation", Action: Action("safe-detach"), Priority: 120, Table: "schedule_run", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id", MySQLOnly: true},
	{ID: "turn.missing_goal", Action: Action("safe-detach"), Priority: 130, Table: "turn", Keys: []string{"id"}, ReferenceTable: "goal", DetachColumn: "goal_id"},
	{ID: "turn.missing_retry_of", Action: Action("safe-detach"), Priority: 130, Table: "turn", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "retry_of"},
	{ID: "turn.missing_run", Action: Action("safe-detach"), Priority: 130, Table: "turn", Keys: []string{"id"}, ReferenceTable: "run", DetachColumn: "run_id"},
	{ID: "turn.missing_started_by_message", Action: Action("safe-detach"), Priority: 130, Table: "turn", Keys: []string{"id"}, ReferenceTable: "message", DetachColumn: "started_by_message_id"},
	{ID: "report_export_job.missing_conversation", Action: Action("safe-detach"), Priority: 140, Table: "report_export_job", Keys: []string{"job_id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id"},
	{ID: "report_run.missing_conversation", Action: Action("safe-detach"), Priority: 160, Table: "report_run", Keys: []string{"report_run_id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id"},
	{ID: "schedule.missing_conversation", Action: Action("safe-detach"), Priority: 170, Table: "schedule", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "conversation_id"},
	{ID: "schedule.missing_goal", Action: Action("safe-detach"), Priority: 170, Table: "schedule", Keys: []string{"id"}, ReferenceTable: "goal", DetachColumn: "goal_id"},
	{ID: "conversation.missing_parent_conversation", Action: Action("safe-detach"), Priority: 190, Table: "conversation", Keys: []string{"id"}, ReferenceTable: "conversation", DetachColumn: "conversation_parent_id"},
	{ID: "conversation.missing_parent_turn", Action: Action("safe-detach"), Priority: 190, Table: "conversation", Keys: []string{"id"}, ReferenceTable: "turn", DetachColumn: "conversation_parent_turn_id"},
	{ID: "conversation.missing_schedule", Action: Action("safe-detach"), Priority: 190, Table: "conversation", Keys: []string{"id"}, ReferenceTable: "schedule", DetachColumn: "schedule_id"},
	{ID: "conversation.missing_schedule_run", Action: Action("safe-detach"), Priority: 190, Table: "conversation", Keys: []string{"id"}, ReferenceTable: "run", DetachColumn: "schedule_run_id"},
	{ID: "tool_execution_claim.missing_turn", Action: Action("safe-delete"), Priority: 1010, Table: "tool_execution_claim", Keys: []string{"claim_key"}, ReferenceTable: "turn"},
	{ID: "turn_queue.missing_conversation", Action: Action("safe-delete"), Priority: 1020, Table: "turn_queue", Keys: []string{"id"}, ReferenceTable: "conversation"},
	{ID: "turn_queue.missing_message", Action: Action("safe-delete"), Priority: 1020, Table: "turn_queue", Keys: []string{"id"}, ReferenceTable: "message"},
	{ID: "turn_queue.missing_turn", Action: Action("safe-delete"), Priority: 1020, Table: "turn_queue", Keys: []string{"id"}, ReferenceTable: "turn"},
	{ID: "model_call.missing_message", Action: Action("safe-delete"), Priority: 1040, Table: "model_call", Keys: []string{"message_id"}, ReferenceTable: "message"},
	{ID: "tool_call.missing_message", Action: Action("safe-delete"), Priority: 1050, Table: "tool_call", Keys: []string{"message_id"}, ReferenceTable: "message"},
	{ID: "generated_file.missing_conversation", Action: Action("safe-delete"), Priority: 1060, Table: "generated_file", Keys: []string{"id"}, ReferenceTable: "conversation"},
	{ID: "report_export_artifact.missing_job", Action: Action("safe-delete"), Priority: 1070, Table: "report_export_artifact", Keys: []string{"artifact_id"}, ReferenceTable: "report_export_job"},
	{ID: "conversation_report_context.missing_active_report_run", Action: Action("safe-delete"), Priority: 1080, Table: "conversation_report_context", Keys: []string{"owner_id", "conversation_id"}, ReferenceTable: "report_run"},
	{ID: "conversation_report_context.missing_conversation", Action: Action("safe-delete"), Priority: 1080, Table: "conversation_report_context", Keys: []string{"owner_id", "conversation_id"}, ReferenceTable: "conversation"},
	{ID: "investigation.missing_conversation", Action: Action("safe-delete"), Priority: 1090, Table: "investigation", Keys: []string{"id"}, ReferenceTable: "conversation", MySQLOnly: true},
	{ID: "message.missing_conversation", Action: Action("safe-delete"), Priority: 1100, Table: "message", Keys: []string{"id"}, ReferenceTable: "conversation"},
	{ID: "schedule_run.missing_schedule", Action: Action("safe-delete"), Priority: 1120, Table: "schedule_run", Keys: []string{"id"}, ReferenceTable: "schedule", MySQLOnly: true},
	{ID: "turn.missing_conversation", Action: Action("safe-delete"), Priority: 1130, Table: "turn", Keys: []string{"id"}, ReferenceTable: "conversation"},
	{ID: "goal.missing_conversation", Action: Action("safe-delete"), Priority: 1180, Table: "goal", Keys: []string{"id"}, ReferenceTable: "conversation"},
	{ID: "call_payload.unused", Action: Action("safe-delete"), Priority: 1210, Table: "call_payload", Keys: []string{"id"}, ReferenceTable: "payload_consumers"},
	{ID: "report_export_job.missing_artifact", Action: Action("report-only"), Priority: 2140, Table: "report_export_job", Keys: []string{"job_id"}, ReferenceTable: "report_export_artifact"},
	{ID: "report_export_job.missing_report_run", Action: Action("report-only"), Priority: 2140, Table: "report_export_job", Keys: []string{"job_id"}, ReferenceTable: "report_run"},
}

// Rules is the static deployment schema contract. Optional legacy tables remain
// MySQL-only even if a SQLite deployment happens to contain those table names.
// Disabled audit/shared-source pseudo-orphans are intentionally absent.
func Rules(mysql bool) []Rule {
	result := make([]Rule, 0, len(staticRules))
	for _, rule := range staticRules {
		if rule.MySQLOnly && !mysql {
			continue
		}
		rule.Keys = append([]string(nil), rule.Keys...)
		if mysql && rule.ID == "conversation.missing_schedule_run" {
			rule.ReferenceTable = "run|schedule_run"
		}
		result = append(result, rule)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Priority == result[j].Priority {
			return result[i].ID < result[j].ID
		}
		return result[i].Priority < result[j].Priority
	})
	return result
}
func FindRule(mysql bool, id string) (Rule, bool) {
	for _, rule := range Rules(mysql) {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}
