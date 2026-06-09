( SELECT COUNT(*) AS pending_count
    FROM tool_approval_queue q
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
      AND q.status = 'pending' )
