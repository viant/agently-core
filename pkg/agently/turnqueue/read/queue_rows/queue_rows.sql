SELECT queue_rows.*
FROM (
    SELECT
        q.id,
        q.conversation_id,
        q.turn_id,
        q.message_id,
        q.queue_seq,
        q.status,
        q.created_at,
        q.updated_at
    FROM turn_queue q
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
    ORDER BY q.queue_seq ASC, q.created_at ASC, q.id ASC
) queue_rows
