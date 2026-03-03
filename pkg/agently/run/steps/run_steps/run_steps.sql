( SELECT
        'model_call' AS step_type,
        m.run_id AS run_id,
        r.conversation_id AS conversation_id,
        m.iteration AS iteration,
        m.message_id AS message_id,
        m.model AS name,
        m.status AS status,
        m.started_at AS started_at,
        m.completed_at AS completed_at,
        m.latency_ms AS latency_ms,
        m.error_message AS error_message
    FROM model_call m
    JOIN run r ON r.id = m.run_id
    WHERE m.run_id IS NOT NULL ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    UNION ALL
    SELECT
        'tool_call' AS step_type,
        t.run_id AS run_id,
        r.conversation_id AS conversation_id,
        t.iteration AS iteration,
        t.message_id AS message_id,
        t.tool_name AS name,
        t.status AS status,
        t.started_at AS started_at,
        t.completed_at AS completed_at,
        t.latency_ms AS latency_ms,
        t.error_message AS error_message
    FROM tool_call t
    JOIN run r ON r.id = t.run_id
    WHERE t.run_id IS NOT NULL ${predicate.Builder().CombineOr($predicate.FilterGroup(1, "AND")).Build("AND")} )