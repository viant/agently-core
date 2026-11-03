SELECT COUNT(*) AS total_count
FROM turn t JOIN conversation c ON c.id=t.conversation_id
WHERE $Allowed = 1
AND ($OwnerID = '' OR (c.created_by_user_id=$OwnerID AND HEX(c.created_by_user_id)=HEX($OwnerID)))
AND ($OwnerIDContains = '' OR INSTR(LOWER(c.created_by_user_id),LOWER($OwnerIDContains)) > 0)
AND ($ConversationID = '' OR c.id=$ConversationID)
AND ($ConversationID <> '' OR (c.created_at >= $From AND c.created_at < $To))
