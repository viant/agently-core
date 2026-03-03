( SELECT t.*
    FROM run t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
    ORDER BY created_at DESC )