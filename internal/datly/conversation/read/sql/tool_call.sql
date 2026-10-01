SELECT toolCall.message_sequence, toolCall.message_id, toolCall.turn_id, toolCall.op_id, toolCall.attempt, toolCall.tool_name, toolCall.tool_kind, toolCall.status, toolCall.request_hash, toolCall.error_code, toolCall.error_message, toolCall.retriable, toolCall.started_at, toolCall.completed_at, toolCall.latency_ms, toolCall.cost, toolCall.trace_id, toolCall.span_id, toolCall.request_payload_id, toolCall.response_payload_id, toolCall.run_id, toolCall.iteration FROM (
SELECT t.*,
          m.sequence AS message_sequence
 FROM tool_call t
 JOIN message m ON m.id = t.message_id
 WHERE (m.type = 'tool_op' OR m.role = 'tool')
 ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("AND")}
) toolCall