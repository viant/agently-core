SELECT runs.* FROM (
 SELECT r.id,r.turn_id,r.attempt,r.resumed_from_run_id,r.status,r.effective_user_id,r.error_code,r.error_message,r.started_at,r.completed_at,
 ROW_NUMBER() OVER (PARTITION BY r.turn_id ORDER BY r.attempt,r.created_at,r.id) AS audit_rank
 FROM run r JOIN turn t ON t.id=r.turn_id JOIN conversation c ON c.id=t.conversation_id
 WHERE $Detail=1 AND $Allowed=1 AND ($OwnerID='' OR c.created_by_user_id=$OwnerID)
 AND ($ConversationID='' OR c.id=$ConversationID) AND ($TurnID='' OR t.id=$TurnID)
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
) runs
WHERE runs.audit_rank<=10001
ORDER BY runs.turn_id,runs.attempt,runs.audit_rank
