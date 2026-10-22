SELECT data_rows.* FROM  (    SELECT r.id, r.schedule_id, r.created_at, r.updated_at,
           r.status, r.error_message, r.lease_owner, r.lease_until,
           r.precondition_ran_at, r.precondition_passed,
           r.precondition_result, r.conversation_id,
           r.conversation_kind, r.scheduled_for, r.started_at,
           r.completed_at,CAST(r.lease_until AS CHAR) AS lease_until_raw,
#if($MaintenanceMode)
CAST(r.retention_activity AS CHAR)
#else
CAST(COALESCE(r.completed_at,r.updated_at,r.created_at) AS CHAR)
#end
 AS activity_raw,
           TRIM(COALESCE((SELECT s.created_by_user_id FROM schedule s WHERE s.id=r.schedule_id),'')) AS maintenance_owner_id
    #if($MaintenanceMode)
    FROM (
      SELECT source_run.id,source_run.schedule_id,source_run.created_at,source_run.updated_at,
        source_run.status,source_run.error_message,source_run.lease_owner,source_run.lease_until,
        source_run.precondition_ran_at,source_run.precondition_passed,source_run.precondition_result,
        source_run.conversation_id,source_run.conversation_kind,source_run.scheduled_for,
        source_run.started_at,source_run.completed_at,
        CASE WHEN retention_root.id IS NULL THEN COALESCE(retention_marker.activity_at,source_run.completed_at,source_run.updated_at,source_run.created_at)
             WHEN retention_marker.activity_at IS NULL OR COALESCE(retention_root.last_activity,retention_root.updated_at,retention_root.created_at) >= retention_marker.activity_at
               THEN COALESCE(retention_root.last_activity,retention_root.updated_at,retention_root.created_at)
             ELSE retention_marker.activity_at END AS retention_activity
      FROM schedule_run source_run
      LEFT JOIN conversation retention_root ON retention_root.id=source_run.conversation_id
        AND COALESCE(retention_root.conversation_parent_id,'')='' AND COALESCE(retention_root.conversation_parent_turn_id,'')=''
      LEFT JOIN (
        SELECT schedule_run_id,MAX(COALESCE(last_activity,updated_at,created_at)) AS activity_at
        FROM conversation WHERE COALESCE(schedule_run_id,'')<>''
          AND COALESCE(conversation_parent_id,'')='' AND COALESCE(conversation_parent_turn_id,'')=''
        GROUP BY schedule_run_id
      ) retention_marker ON retention_marker.schedule_run_id=source_run.id
    ) r
#else
    FROM schedule_run r
#end
    WHERE $Trusted
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    AND (NOT $MaintenanceMode OR (TRIM(COALESCE(r.schedule_id,''))<>'' AND EXISTS(SELECT 1 FROM schedule s WHERE s.id=r.schedule_id) AND NOT EXISTS(SELECT 1 FROM run current_run WHERE current_run.id=r.id AND COALESCE(current_run.run_kind,'execution')='execution')))
    #if($MaintenanceMode)
    AND (${View.TimestampSecondsUTC("r.retention_activity")} < $MaintenanceBeforeSecond OR (${View.TimestampSecondsUTC("r.retention_activity")} = $MaintenanceBeforeSecond AND ${View.TimestampNanoseconds("r.retention_activity")} <= $MaintenanceBeforeNano))
    #if($Has.MaintenanceAfterId)
    AND (${View.TimestampSecondsUTC("r.retention_activity")} > $MaintenanceAfterActivity OR (${View.TimestampSecondsUTC("r.retention_activity")} = $MaintenanceAfterActivity AND (${View.TimestampNanoseconds("r.retention_activity")} > $MaintenanceAfterNano OR (${View.TimestampNanoseconds("r.retention_activity")} = $MaintenanceAfterNano AND HEX(r.id)>HEX($MaintenanceAfterId)))))
    #end
    #end
    ORDER BY 
       
    #if($MaintenanceMode)
       ${View.TimestampSecondsUTC("r.retention_activity")} ASC,${View.TimestampNanoseconds("r.retention_activity")} ASC,
    #end
       
       CASE WHEN $MaintenanceMode THEN HEX(r.id) END ASC,
       CASE WHEN NOT $MaintenanceMode THEN r.created_at END,CASE WHEN NOT $MaintenanceMode THEN r.id END
)  data_rows