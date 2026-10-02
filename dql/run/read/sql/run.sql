
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
    ORDER BY 
       
    #if($ReadMode == "scheduledMaintenance")
       ${View.TimestampSecondsUTC("COALESCE(t.completed_at,t.updated_at,t.created_at)")} ASC,${View.TimestampNanoseconds("COALESCE(t.completed_at,t.updated_at,t.created_at)")} ASC,
    #end
       
       CASE WHEN $ReadMode='scheduledMaintenance' THEN HEX(t.id) END ASC, CASE WHEN $ReadMode IN ('schedulerList','schedulerRuns','schedulerDue') THEN t.started_at END DESC,
       CASE WHEN $ReadMode='stale' THEN COALESCE(t.last_heartbeat_at,t.started_at,t.created_at) END DESC,
       CASE WHEN $ReadMode NOT IN ('schedulerList','schedulerRuns','schedulerDue','scheduledMaintenance') THEN t.created_at END DESC, CASE WHEN $ReadMode IN ('stale','schedulerList','schedulerRuns','schedulerDue') THEN t.id END DESC
