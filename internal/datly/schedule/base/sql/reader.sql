SELECT schedule.id, schedule.name, schedule.description, schedule.created_by_user_id, schedule.visibility, schedule.internal, schedule.conversation_id, schedule.goal_id, schedule.agent_ref, schedule.model_override, schedule.user_cred_url, schedule.enabled, schedule.start_at, schedule.end_at, schedule.schedule_type, schedule.cron_expr, schedule.interval_seconds, schedule.timezone, schedule.timeout_seconds, schedule.task_prompt_uri, schedule.task_prompt, schedule.next_run_at, schedule.last_run_at, schedule.last_status, schedule.last_error, schedule.lease_owner, schedule.created_at, schedule.updated_at, schedule.lease_until_raw FROM  (
SELECT t.*, CAST(t.lease_until AS CHAR) AS lease_until_raw FROM schedule t WHERE 1=1
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
     AND ($InternalMode OR (COALESCE(t.internal,0)=0 AND
         (COALESCE(t.visibility,'') <> 'private' OR t.created_by_user_id=NULLIF($VisibilitySubject,''))))
)  schedule