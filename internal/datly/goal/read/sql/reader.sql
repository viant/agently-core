SELECT goal.id, goal.conversation_id, goal.objective, goal.status, goal.status_reason, goal.pause_reason, goal.controller_spec, goal.token_budget, goal.tokens_used, goal.time_used_seconds, goal.autonomous_turns_used, goal.consecutive_no_progress, goal.last_continuation_fingerprint, goal.created_at, goal.updated_at FROM  (
SELECT t.*
  FROM goal t
  ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}

#if($LockRows) ${View.ForUpdate()} #end  
)  goal