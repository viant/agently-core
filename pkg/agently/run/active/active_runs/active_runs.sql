( SELECT t.*
    FROM run t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND t.status IN ('running', 'queued', 'pending')
    ORDER BY t.created_at DESC
    LIMIT 1 )