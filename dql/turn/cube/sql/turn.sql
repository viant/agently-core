
SELECT t.conversation_id,
       t.status,
       t.origin,
       COUNT(*) AS record_count,
       COUNT(CASE WHEN t.status='queued' THEN 1 END) AS queued_count,
       COUNT(CASE WHEN t.origin='controller' THEN 1 END) AS controller_count
FROM turn t WHERE $Trusted
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
GROUP BY t.conversation_id,t.status,t.origin
