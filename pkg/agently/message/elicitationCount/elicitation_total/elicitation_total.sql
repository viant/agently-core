( SELECT COUNT(*) AS pending_count
    FROM message m
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND m.elicitation_id IS NOT NULL
      AND m.status = 'pending' )
