
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

