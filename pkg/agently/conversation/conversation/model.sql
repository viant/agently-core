SELECT
    m.conversation_id AS conversation_id,
    mc.provider,
    mc.model,
    CASE
      WHEN LOWER(COALESCE(m.mode, '')) = 'router' THEN 'intake'
      WHEN LOWER(COALESCE(m.mode, '')) IN ('intake', 'sidecar', 'summary', 'narrator', 'worker')
        THEN LOWER(m.mode)
      WHEN LOWER(COALESCE(t.agent_id_used, '')) IN ('intake_sidecar', 'agent_selector', 'agent-selector', 'tool_router', 'planner_pass')
        THEN 'sidecar'
      ELSE 'react'
    END AS execution_role,
    SUM(COALESCE(mc.prompt_tokens, 0))                                  AS prompt_tokens,
    SUM(COALESCE(mc.prompt_cached_tokens, 0))                           AS prompt_cached_tokens,
    SUM(COALESCE(mc.prompt_audio_tokens, 0))                            AS prompt_audio_tokens,
    SUM(COALESCE(mc.completion_tokens, 0))                               AS completion_tokens,
    SUM(COALESCE(mc.completion_reasoning_tokens, 0))                    AS completion_reasoning_tokens,
    SUM(COALESCE(mc.completion_audio_tokens, 0))                         AS completion_audio_tokens,
    SUM(COALESCE(mc.completion_accepted_prediction_tokens, 0))          AS completion_accepted_prediction_tokens,
    SUM(COALESCE(mc.completion_rejected_prediction_tokens, 0))          AS completion_rejected_prediction_tokens,
    SUM(COALESCE(mc.total_tokens, 0))                                    AS total_tokens,
    SUM(COALESCE(mc.cost, 0))                                    AS cost
  FROM model_call mc
  JOIN message m ON m.id = mc.message_id
  LEFT JOIN turn t ON t.id = m.turn_id
  $View.ParentJoinOn("WHERE","m.conversation_id")
  GROUP BY m.conversation_id, mc.provider, mc.model, execution_role
