SELECT run_rows.lease_until_raw, run_rows.heartbeat_raw, run_rows.activity_raw, run_rows.maintenance_owner_id, run_rows.agent_id, run_rows.attempt, run_rows.auth_audience, run_rows.auth_authority, run_rows.checkpoint_data, run_rows.checkpoint_message_id, run_rows.checkpoint_response_id, run_rows.completed_at, run_rows.conversation_id, run_rows.conversation_kind, run_rows.created_at, run_rows.effective_user_id, run_rows.error_code, run_rows.error_message, run_rows.heartbeat_interval_sec, run_rows.id, run_rows.iteration, run_rows.last_heartbeat_at, run_rows.lease_owner, run_rows.lease_until, run_rows.max_iterations, run_rows.model, run_rows.model_provider, run_rows.precondition_passed, run_rows.precondition_ran_at, run_rows.precondition_result, run_rows.resumed_from_run_id, run_rows.schedule_id, run_rows.scheduled_for, run_rows.security_context, run_rows.started_at, run_rows.status, run_rows.turn_id, run_rows.updated_at, run_rows.usage_completion_tokens, run_rows.usage_cost, run_rows.usage_prompt_tokens, run_rows.usage_total_tokens, run_rows.user_cred_url, run_rows.worker_host, run_rows.worker_id, run_rows.worker_pid FROM  (
SELECT t.*,CAST(t.lease_until AS CHAR) AS lease_until_raw,CAST(t.last_heartbeat_at AS CHAR) AS heartbeat_raw,CAST(COALESCE(t.completed_at,t.updated_at,t.created_at) AS CHAR) AS activity_raw,
 TRIM(COALESCE((SELECT NULLIF(TRIM(s.created_by_user_id),'') FROM schedule s WHERE s.id=t.schedule_id),t.effective_user_id,'')) AS maintenance_owner_id
    FROM run t WHERE 1=1
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    AND ($InternalMode OR COALESCE(t.effective_user_id,'')='' OR t.effective_user_id=NULLIF($VisibilitySubject,''))
    AND ($ReadMode <> 'schedulerList' OR (t.schedule_id IS NOT NULL AND EXISTS (
      SELECT 1 FROM schedule s WHERE s.id=t.schedule_id AND COALESCE(s.internal,0)=0
      AND (s.visibility IS NULL OR s.visibility<>'private' OR s.created_by_user_id=NULLIF($VisibilitySubject,'')))))
    AND ($ReadMode <> 'schedulerRuns' OR EXISTS (
      SELECT 1 FROM schedule s WHERE s.id=t.schedule_id
      AND (s.visibility IS NULL OR s.visibility<>'private' OR s.created_by_user_id=NULLIF($VisibilitySubject,''))))
    AND ($ReadMode <> 'scheduledMaintenance' OR (TRIM(COALESCE(t.schedule_id,''))<>'' AND EXISTS(SELECT 1 FROM schedule s WHERE s.id=t.schedule_id)))
    #if($ReadMode == "scheduledMaintenance")
    AND (${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} < $MaintenanceBeforeSecond OR (${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} = $MaintenanceBeforeSecond AND ${View.TimestampNanoseconds("COALESCE(t.completed_at,t.updated_at,t.created_at)")} <= $MaintenanceBeforeNano))
    #if($Has.MaintenanceAfterId)
    AND (${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} > $MaintenanceAfterActivity OR (${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} = $MaintenanceAfterActivity AND (${View.TimestampNanoseconds("COALESCE(t.completed_at,t.updated_at,t.created_at)")} > $MaintenanceAfterNano OR (${View.TimestampNanoseconds("COALESCE(t.completed_at,t.updated_at,t.created_at)")} = $MaintenanceAfterNano AND HEX(t.id)>HEX($MaintenanceAfterId)))))
    #end
    #end
    AND ($ReadMode <> 'stale'   OR t.status='running')
    AND ($ReadMode <> 'active' OR t.id=(
       SELECT t.id FROM run t WHERE t.status IN ('running','queued','pending')
       ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
       ORDER BY t.created_at DESC LIMIT 1))
    ORDER BY CASE WHEN $LockRows THEN t.id END ASC,
       
    #if($ReadMode == "scheduledMaintenance")
       ${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} ASC,${View.TimestampNanoseconds("COALESCE(t.completed_at,t.updated_at,t.created_at)")} ASC,
    #end
       
       CASE WHEN $ReadMode='scheduledMaintenance' THEN HEX(t.id) END ASC, CASE WHEN $ReadMode IN ('schedulerList','schedulerRuns','schedulerDue') THEN t.started_at END DESC,
       CASE WHEN $ReadMode='stale' THEN COALESCE(t.last_heartbeat_at,t.started_at,t.created_at) END DESC,
       CASE WHEN $ReadMode NOT IN ('schedulerList','schedulerRuns','schedulerDue','scheduledMaintenance') THEN t.created_at END DESC, CASE WHEN $ReadMode IN ('stale','schedulerList','schedulerRuns','schedulerDue') THEN t.id END DESC
    #if($LockRows) ${View.ForUpdate()} #end  
)  run_rows