SELECT t.id,
       t.status,
       t.conversation_id,
       t.turn_id,
       t.schedule_id,
       t.resumed_from_run_id,
       t.conversation_kind,
       CAST(t.lease_until AS CHAR) AS lease_until_raw,
       CAST(t.last_heartbeat_at AS CHAR) AS heartbeat_raw,
       t.heartbeat_interval_sec,
       CAST(COALESCE(t.completed_at,t.updated_at,t.created_at) AS CHAR) AS activity_raw
FROM run t
WHERE COALESCE(t.run_kind,'execution')='execution'
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
ORDER BY t.created_at DESC, t.id DESC
