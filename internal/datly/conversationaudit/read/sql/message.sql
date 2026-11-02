SELECT message.* FROM (
 SELECT m.*, ROW_NUMBER() OVER (PARTITION BY m.turn_id ORDER BY m.sequence,m.created_at,m.id) AS audit_rank
 FROM message m JOIN turn t ON t.id=m.turn_id JOIN conversation c ON c.id=t.conversation_id
 WHERE $Detail=1 AND $Allowed=1 AND ($OwnerID='' OR c.created_by_user_id=$OwnerID)
 AND ($ConversationID='' OR c.id=$ConversationID) AND ($TurnID='' OR t.id=$TurnID)
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
) message
WHERE message.audit_rank<=10001
ORDER BY message.turn_id,message.sequence,message.created_at,message.id
