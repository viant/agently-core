( SELECT m.*
    FROM message m
    ${predicate.Builder().CombineOr($predicate.FilterGroup(4, "AND")).Build("WHERE")}
    ORDER BY m.created_at DESC, m.id DESC )