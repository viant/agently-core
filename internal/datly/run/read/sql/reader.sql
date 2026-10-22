SELECT run_rows.* FROM  (SELECT
#if($ReadMode == "scheduledMaintenance")
t.id,
#else
t.*,
#end
CAST(t.lease_until AS CHAR) AS lease_until_raw,CAST(t.last_heartbeat_at AS CHAR) AS heartbeat_raw,
#if($ReadMode == "scheduledMaintenance")
CAST(t.retention_activity AS CHAR)
#else
CAST(COALESCE(t.completed_at,t.updated_at,t.created_at) AS CHAR)
#end
 AS activity_raw,
 TRIM(COALESCE((SELECT NULLIF(TRIM(s.created_by_user_id),'') FROM schedule s WHERE s.id=t.schedule_id),t.effective_user_id,'')) AS maintenance_owner_id
    #if($ReadMode == "scheduledMaintenance")
    FROM (
      SELECT source_run.id,source_run.run_kind,source_run.effective_user_id,source_run.schedule_id,
        source_run.lease_until,source_run.last_heartbeat_at,source_run.status,
        source_run.created_at,source_run.updated_at,source_run.completed_at,source_run.started_at,
        CASE WHEN retention_root.id IS NULL THEN COALESCE(retention_marker.activity_at,source_run.completed_at,source_run.updated_at,source_run.created_at)
             WHEN retention_marker.activity_at IS NULL OR COALESCE(retention_root.last_activity,retention_root.updated_at,retention_root.created_at) >= retention_marker.activity_at
               THEN COALESCE(retention_root.last_activity,retention_root.updated_at,retention_root.created_at)
             ELSE retention_marker.activity_at END AS retention_activity
      FROM run source_run
      LEFT JOIN conversation retention_root ON retention_root.id=source_run.conversation_id
        AND COALESCE(retention_root.conversation_parent_id,'')='' AND COALESCE(retention_root.conversation_parent_turn_id,'')=''
      LEFT JOIN (
        SELECT schedule_run_id,MAX(COALESCE(last_activity,updated_at,created_at)) AS activity_at
        FROM conversation WHERE COALESCE(schedule_run_id,'')<>''
          AND COALESCE(conversation_parent_id,'')='' AND COALESCE(conversation_parent_turn_id,'')=''
        GROUP BY schedule_run_id
      ) retention_marker ON retention_marker.schedule_run_id=source_run.id
    ) t
#else
    FROM run t
#end
    WHERE COALESCE(t.run_kind,'execution')='execution'
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
    AND (${View.TimestampSecondsUTC("t.retention_activity")} < $MaintenanceBeforeSecond OR (${View.TimestampSecondsUTC("t.retention_activity")} = $MaintenanceBeforeSecond AND ${View.TimestampNanoseconds("t.retention_activity")} <= $MaintenanceBeforeNano))
    #if($Has.MaintenanceAfterId)
    AND (${View.TimestampSecondsUTC("t.retention_activity")} > $MaintenanceAfterActivity OR (${View.TimestampSecondsUTC("t.retention_activity")} = $MaintenanceAfterActivity AND (${View.TimestampNanoseconds("t.retention_activity")} > $MaintenanceAfterNano OR (${View.TimestampNanoseconds("t.retention_activity")} = $MaintenanceAfterNano AND HEX(t.id)>HEX($MaintenanceAfterId)))))
    #end
    #end
    AND ($ReadMode <> 'stale'   OR t.status='running')
    AND ($ReadMode <> 'active' OR t.id=(
       SELECT t.id FROM run t WHERE COALESCE(t.run_kind,'execution')='execution' AND t.status IN ('running','queued','pending')
       ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
       ORDER BY t.created_at DESC LIMIT 1))
    ORDER BY 
       
    #if($ReadMode == "scheduledMaintenance")
       ${View.TimestampSecondsUTC("t.retention_activity")} ASC,${View.TimestampNanoseconds("t.retention_activity")} ASC,
    #end
       
       CASE WHEN $ReadMode='scheduledMaintenance' THEN HEX(t.id) END ASC, CASE WHEN $ReadMode IN ('schedulerList','schedulerRuns','schedulerDue') THEN t.started_at END DESC,
       CASE WHEN $ReadMode='stale' THEN COALESCE(t.last_heartbeat_at,t.started_at,t.created_at) END DESC,
       CASE WHEN $ReadMode NOT IN ('schedulerList','schedulerRuns','schedulerDue','scheduledMaintenance') THEN t.created_at END DESC, CASE WHEN $ReadMode IN ('stale','schedulerList','schedulerRuns','schedulerDue') THEN t.id END DESC
)  run_rows