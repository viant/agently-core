SELECT c.id,
       c.conversation_parent_id,
       c.conversation_parent_turn_id,
       c.scheduled,
       c.schedule_id,
       c.schedule_run_id,
       c.schedule_kind,
       CAST(c.created_at AS CHAR) AS created_at_raw,
       CAST(COALESCE(c.last_activity,c.updated_at,c.created_at) AS CHAR) AS activity_raw
FROM conversation c
WHERE 1 = 1 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
ORDER BY c.id DESC
