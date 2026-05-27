SELECT o.*
FROM (
    SELECT
        q.*,
        CASE
            WHEN LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) = 'timed_out' THEN q.timed_out_at
            WHEN LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) = 'executed' THEN q.executed_at
            WHEN LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) = 'approved' THEN q.approved_at
            WHEN LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) = 'rejected' THEN q.approved_at
            WHEN LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) = 'canceled' THEN q.approved_at
            WHEN q.updated_at IS NOT NULL THEN q.updated_at
            ELSE q.created_at
        END AS transition_at
    FROM tool_approval_queue q
    WHERE LOWER(CASE WHEN q.status IS NULL THEN '' ELSE q.status END) IN ('approved', 'rejected', 'canceled', 'executed', 'failed', 'timed_out')
) o
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY o.transition_at ASC, o.id ASC
