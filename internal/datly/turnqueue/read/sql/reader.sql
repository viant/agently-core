SELECT queue_rows.id, queue_rows.conversation_id, queue_rows.turn_id, queue_rows.message_id, queue_rows.queue_seq, queue_rows.status, queue_rows.created_at, queue_rows.updated_at FROM  (
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

#if($LockRows) ${View.ForUpdate()} #end  
) queue_rows
)  queue_rows