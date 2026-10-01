SELECT toolCallLinks.message_id, toolCallLinks.op_id, toolCallLinks.trace_id FROM (
SELECT message_id, op_id, trace_id  FROM tool_call t
) toolCallLinks