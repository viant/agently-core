SELECT q.* FROM  (
SELECT q.user_id,
       q.conversation_id,
       q.tool_name,
       q.status,
       q.decision,
       COUNT(*) AS total_count,
       COUNT(CASE WHEN q.status='pending' THEN 1 END) AS pending_count,
       COUNT(CASE WHEN LOWER(q.status) IN ('approved','rejected','canceled','executed','failed','timed_out') THEN 1 END) AS outcome_count,
       COUNT(CASE WHEN LOWER(q.status)='timed_out' THEN 1 END) AS timed_out_count
FROM tool_approval_queue q WHERE ($Internal OR q.user_id=NULLIF($VisibilitySubject,''))
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
GROUP BY q.user_id,q.conversation_id,q.tool_name,q.status,q.decision
)  q