( SELECT
        c.*,
        (SELECT id FROM turn t WHERE t.conversation_id = c.id ORDER BY t.created_at DESC, t.id DESC LIMIT 1) AS last_turn_id,
        CASE
            WHEN LOWER(COALESCE(c.status, '')) IN ('failed', 'error', 'terminated') THEN 'error'
            WHEN LOWER(COALESCE(c.status, '')) IN ('canceled', 'cancelled') THEN 'canceled'
            WHEN LOWER(COALESCE(c.status, '')) IN ('completed', 'succeeded', 'success', 'done', 'compacted', 'pruned') THEN 'done'
            WHEN LOWER(COALESCE(c.status, '')) IN ('waiting_for_user', 'blocked') THEN 'elicitation'
            WHEN LOWER(COALESCE(c.status, '')) IN ('running', 'thinking', 'processing', 'in_progress', 'queued', 'pending', 'open') THEN 'executing'
            ELSE ''
        END AS stage
    FROM conversation c
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
    ORDER BY COALESCE(c.last_activity, c.updated_at, c.created_at) DESC, c.id DESC )
