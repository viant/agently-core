( SELECT t.*
    FROM run t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND t.status = 'running'
      AND (t.resumed_from_run_id IS NULL OR TRIM(t.resumed_from_run_id) = '')
    ORDER BY t.last_heartbeat_at ASC )
