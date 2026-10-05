SELECT t.* FROM  (
SELECT t.schedule_id,
       t.status,
       t.conversation_kind,
       t.effective_user_id,
       COUNT(*) AS record_count,
       SUM(t.usage_total_tokens) AS total_tokens,
       SUM(t.usage_cost) AS total_cost
FROM run t WHERE COALESCE(t.run_kind,'execution')='execution'
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
 AND ($InternalMode OR COALESCE(t.effective_user_id,'')='' OR t.effective_user_id=NULLIF($VisibilitySubject,''))
 AND ($ReportMode<>'scheduler' OR (t.schedule_id IS NOT NULL AND EXISTS(
  SELECT 1 FROM schedule s WHERE s.id=t.schedule_id AND COALESCE(s.internal,0)=0
  AND (s.visibility IS NULL OR s.visibility<>'private' OR s.created_by_user_id=NULLIF($VisibilitySubject,'')))))
GROUP BY t.schedule_id,t.status,t.conversation_kind,t.effective_user_id
)  t