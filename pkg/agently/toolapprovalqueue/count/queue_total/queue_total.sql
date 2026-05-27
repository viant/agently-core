( SELECT COUNT(*) AS total_count
    FROM tool_approval_queue q
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")} )
