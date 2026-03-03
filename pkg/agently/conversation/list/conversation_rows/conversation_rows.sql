( SELECT
        c.*,
        (SELECT id FROM turn t WHERE t.conversation_id = c.id ORDER BY t.created_at DESC, t.id DESC LIMIT 1) AS last_turn_id,
        '' AS stage
    FROM conversation c
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
    ORDER BY c.created_at DESC, c.id DESC )