( SELECT t.*
    FROM run t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND t.status = 'running'
    ORDER BY t.last_heartbeat_at ASC )