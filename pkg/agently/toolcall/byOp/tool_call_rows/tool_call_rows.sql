( SELECT
    t.message_id,
    t.turn_id,
    t.op_id,
    t.trace_id,
    t.response_payload_id
  FROM tool_call t
  JOIN message m ON m.id = t.message_id
  WHERE m.conversation_id = $criteria.AppendBinding($Unsafe.ConversationId)
    AND t.op_id = $criteria.AppendBinding($Unsafe.OpId) )