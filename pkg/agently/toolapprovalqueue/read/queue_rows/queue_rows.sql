SELECT q.*
FROM tool_approval_queue q
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY q.created_at ASC, q.id ASC
