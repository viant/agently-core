( SELECT t.*
    FROM run t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND t.status = 'running'
    ORDER BY COALESCE(t.last_heartbeat_at, t.created_at) DESC, t.created_at DESC )
