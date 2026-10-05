
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
    WHERE 1=1
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    #if($NativeQueuedOnly)
    AND q.status = 'queued'
    AND EXISTS (SELECT 1 FROM turn t WHERE t.id = q.turn_id
                AND t.conversation_id = q.conversation_id AND t.status = 'queued')
    #end
    ORDER BY q.queue_seq ASC, q.created_at ASC, q.id ASC

) queue_rows
