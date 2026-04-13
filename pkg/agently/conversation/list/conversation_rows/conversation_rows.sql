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
    WHERE (c.conversation_parent_id IS NULL OR (c.conversation_parent_turn_id IS NOT NULL AND EXISTS (SELECT 1 FROM conversation p WHERE p.id = c.conversation_parent_id) AND EXISTS (SELECT 1 FROM turn pt WHERE pt.id = c.conversation_parent_turn_id AND pt.conversation_id = c.conversation_parent_id)))
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY COALESCE(c.last_activity, c.updated_at, c.created_at) DESC, c.id DESC )
