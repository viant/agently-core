SELECT queue_rows.id, queue_rows.user_id, queue_rows.conversation_id, queue_rows.turn_id, queue_rows.message_id, queue_rows.tool_name, queue_rows.title, queue_rows.arguments, queue_rows.metadata, queue_rows.status, queue_rows.decision, queue_rows.expires_at, queue_rows.timed_out_at, queue_rows.approved_by_user_id, queue_rows.approved_at, queue_rows.executed_at, queue_rows.error_message, queue_rows.created_at, queue_rows.updated_at, queue_rows.transition_at FROM  (
 SELECT q.* FROM (
 SELECT q.*, CASE
 WHEN LOWER(COALESCE(q.status,''))='timed_out' THEN q.timed_out_at
 WHEN LOWER(COALESCE(q.status,''))='executed' THEN q.executed_at
 WHEN LOWER(COALESCE(q.status,'')) IN ('approved','rejected','canceled') THEN q.approved_at
 WHEN q.updated_at IS NOT NULL THEN q.updated_at ELSE q.created_at END AS transition_at
 FROM tool_approval_queue q
 ) q WHERE ($Internal OR q.user_id=NULLIF($VisibilitySubject,''))
 AND ($ReadMode<>'outcome' OR LOWER(COALESCE(q.status,'')) IN ('approved','rejected','canceled','executed','failed','timed_out'))
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
 ORDER BY CASE WHEN $ReadMode='outcome' THEN q.transition_at END ASC,
 CASE WHEN $ReadMode='outcome' THEN q.id END ASC,
 CASE WHEN $ReadMode='rows' THEN q.created_at END DESC,
 CASE WHEN $ReadMode='rows' THEN q.id END DESC

#if($LockRows) ${View.ForUpdate()} #end  
)  queue_rows