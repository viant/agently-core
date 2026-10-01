SELECT f.* FROM  (
SELECT f.conversation_id,
       f.provider,
       f.model,
       f.model_kind,
       f.status,
       f.execution_role,
       COUNT(*) AS record_count,
       CASE WHEN COUNT(f.cost)=COUNT(*) THEN SUM(f.cost) ELSE NULL END AS cost,
       SUM(COALESCE(f.prompt_tokens,0)) AS prompt_tokens,
       SUM(COALESCE(f.prompt_cached_tokens,0)) AS prompt_cached_tokens,
       SUM(COALESCE(f.prompt_audio_tokens,0)) AS prompt_audio_tokens,
       SUM(COALESCE(f.completion_tokens,0)) AS completion_tokens,
       SUM(COALESCE(f.completion_reasoning_tokens,0)) AS completion_reasoning_tokens,
       SUM(COALESCE(f.completion_audio_tokens,0)) AS completion_audio_tokens,
       SUM(COALESCE(f.completion_accepted_prediction_tokens,0)) AS completion_accepted_prediction_tokens,
       SUM(COALESCE(f.completion_rejected_prediction_tokens,0)) AS completion_rejected_prediction_tokens,
       SUM(COALESCE(f.total_tokens,0)) AS total_tokens
FROM (SELECT mc.*,m.conversation_id,
 CASE
  WHEN LOWER(COALESCE(m.mode,''))='router' THEN 'intake'
  WHEN LOWER(COALESCE(m.mode,'')) IN ('intake','sidecar','summary','narrator','worker') THEN LOWER(m.mode)
  WHEN LOWER(COALESCE(t.agent_id_used,'')) IN ('intake_sidecar','agent_selector','agent-selector','tool_router','planner_pass') THEN 'sidecar'
  ELSE 'react' END AS execution_role
 FROM model_call mc JOIN message m ON m.id=mc.message_id LEFT JOIN turn t ON t.id=m.turn_id) f
WHERE 1=1
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
 AND ($Internal OR EXISTS(SELECT 1 FROM conversation c WHERE c.id=f.conversation_id
  AND (COALESCE(c.visibility,'')<>'private' OR c.created_by_user_id=NULLIF($VisibilitySubject,''))))
GROUP BY f.conversation_id,f.provider,f.model,f.model_kind,f.status,f.execution_role
)  f