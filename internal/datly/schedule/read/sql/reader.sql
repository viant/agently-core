SELECT schedule.agent_ref, schedule.created_at, schedule.created_by_user_id, schedule.visibility, schedule.cron_expr, schedule.description, schedule.enabled, schedule.end_at, schedule.goal_id, schedule.id, schedule.internal, schedule.interval_seconds, schedule.last_error, schedule.last_run_at, schedule.last_status, schedule.lease_owner, schedule.lease_until, schedule.lease_until_raw, schedule.model_override, schedule.conversation_id, schedule.user_cred_url, schedule.name, schedule.next_run_at, schedule.schedule_type, schedule.start_at, schedule.task_prompt, schedule.task_prompt_uri, schedule.timeout_seconds, schedule.timezone, schedule.updated_at FROM  (
SELECT t.*, CAST(t.lease_until AS CHAR) AS lease_until_raw FROM schedule t WHERE 1=1
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
     AND ($InternalMode OR (COALESCE(t.internal,0)=0 AND
         (COALESCE(t.visibility,'') <> 'private' OR t.created_by_user_id=NULLIF($VisibilitySubject,''))))
     #if($LockRows) ORDER BY t.id ${View.ForUpdate()} #end
)  schedule