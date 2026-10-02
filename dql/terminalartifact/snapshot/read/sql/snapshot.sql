
WITH ranked_terminal_turns AS (
    SELECT t.id, t.conversation_id, t.run_id, t.status, t.error_message, t.created_at,
           ROW_NUMBER() OVER (ORDER BY t.created_at DESC, t.id DESC) AS terminal_rank
    FROM turn t
    WHERE t.created_at >= $TerminalSince
      AND LOWER(TRIM(t.status)) IN ('failed', 'succeeded', 'canceled')
),
terminal_turns AS (
    SELECT id, conversation_id, run_id, status, error_message, created_at
    FROM ranked_terminal_turns
    WHERE terminal_rank <= $TerminalTurnLimit
),
execution_artifacts AS (
    SELECT 'model_call' AS artifact_kind,
           mc.message_id AS artifact_id,
           mc.turn_id AS artifact_turn_id,
           mc.run_id AS artifact_run_id
    FROM model_call mc
    WHERE LOWER(TRIM(COALESCE(mc.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
    UNION ALL
    SELECT 'tool_call' AS artifact_kind,
           tc.message_id AS artifact_id,
           tc.turn_id AS artifact_turn_id,
           tc.run_id AS artifact_run_id
    FROM tool_call tc
    WHERE LOWER(TRIM(COALESCE(tc.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
),
candidate_rows AS (
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'direct_turn' AS link_mode,
           COALESCE(a.artifact_turn_id, '') AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           1 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.id = a.artifact_turn_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'message_turn' AS link_mode,
           COALESCE(m.turn_id, '') AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           2 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN message m ON m.id = a.artifact_id
    JOIN terminal_turns tt ON tt.id = m.turn_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'run' AS link_mode,
           '' AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           3 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.run_id IS NOT NULL
                          AND tt.run_id <> ''
                          AND tt.run_id = a.artifact_run_id
    UNION ALL
    SELECT a.artifact_kind, a.artifact_id, tt.conversation_id, tt.id AS turn_id,
           'legacy_run' AS link_mode,
           '' AS expected_link,
           COALESCE(a.artifact_run_id, '') AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           4 AS link_rank, tt.created_at AS terminal_created_at
    FROM execution_artifacts a
    JOIN terminal_turns tt ON tt.id = a.artifact_run_id
    UNION ALL
    SELECT 'message' AS artifact_kind, m.id AS artifact_id,
           tt.conversation_id, tt.id AS turn_id,
           'direct_turn' AS link_mode,
           COALESCE(m.turn_id, '') AS expected_link,
           '' AS expected_run,
           tt.status AS terminal_status, COALESCE(tt.error_message, '') AS terminal_error,
           1 AS link_rank, tt.created_at AS terminal_created_at
    FROM message m
    JOIN terminal_turns tt ON tt.id = m.turn_id
    WHERE (LOWER(TRIM(m.role)) = 'tool' OR LOWER(TRIM(m.type)) = 'tool_op')
      AND LOWER(TRIM(COALESCE(m.status, ''))) NOT IN ('completed', 'failed', 'canceled', 'cancelled', 'succeeded')
)
SELECT artifact_kind, artifact_id, conversation_id, turn_id, link_mode,
       expected_link, expected_run, terminal_status, terminal_error, link_rank, terminal_created_at
FROM candidate_rows
ORDER BY artifact_kind ASC, artifact_id ASC, link_rank ASC,
         terminal_created_at DESC, turn_id DESC
