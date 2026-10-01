SELECT * FROM (
SELECT
    m.id,
    m.parent_message_id,
    m.created_at,
    m.sequence,
    m.type,
    m.content,
    m.tool_name,
    m.iteration,
    NULLIF(m.linked_conversation_id, '') AS linked_conversation_id
 FROM message m
 WHERE ($Internal OR EXISTS(SELECT 1 FROM conversation scope_c WHERE scope_c.id=m.conversation_id AND (COALESCE(scope_c.visibility,'')<>'private' OR scope_c.created_by_user_id=NULLIF($VisibilitySubject,'')))) AND m.parent_message_id IS NOT NULL
   AND (m.type = 'tool_op' OR m.role = 'tool')
 ORDER BY m.sequence, m.created_at
) toolMessage