SELECT
    m.id,
    m.parent_message_id,
    m.created_at,
    m.type,
    m.content,
    m.tool_name,
    m.iteration
 FROM message m
 WHERE m.parent_message_id IS NOT NULL
   AND (m.type = 'tool_op' OR m.role = 'tool')