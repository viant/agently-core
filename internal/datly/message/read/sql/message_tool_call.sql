SELECT messageToolCall.message_sequence, messageToolCall.message_id, messageToolCall.turn_id, messageToolCall.op_id, messageToolCall.attempt, messageToolCall.tool_name, messageToolCall.tool_kind, messageToolCall.status, messageToolCall.request_hash, messageToolCall.error_code, messageToolCall.error_message, messageToolCall.retriable, messageToolCall.started_at, messageToolCall.completed_at, messageToolCall.latency_ms, messageToolCall.cost, messageToolCall.trace_id, messageToolCall.span_id, messageToolCall.request_payload_id, messageToolCall.response_payload_id, messageToolCall.run_id, messageToolCall.iteration FROM (
SELECT t.*,
          m.sequence AS message_sequence
 FROM tool_call t
 JOIN message m ON m.id = t.message_id
 WHERE ($Internal OR EXISTS(SELECT 1 FROM conversation scope_c WHERE scope_c.id=m.conversation_id AND (COALESCE(scope_c.visibility,'')<>'private' OR scope_c.created_by_user_id=NULLIF($VisibilitySubject,'')))) AND (m.type = 'tool_op' OR m.role = 'tool')
 ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("AND")}
) messageToolCall