
    SELECT r.id, r.schedule_id, r.created_at, r.updated_at,
           r.status, r.error_message, r.lease_owner, r.lease_until,
           r.precondition_ran_at, r.precondition_passed,
           r.precondition_result, r.conversation_id,
           r.conversation_kind, r.scheduled_for, r.started_at,
           r.completed_at,CAST(r.lease_until AS CHAR) AS lease_until_raw,CAST(COALESCE(r.completed_at,r.updated_at,r.created_at) AS CHAR) AS activity_raw,
           TRIM(COALESCE((SELECT s.created_by_user_id FROM schedule s WHERE s.id=r.schedule_id),'')) AS maintenance_owner_id
    FROM schedule_run r
    WHERE $Trusted
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    AND (NOT $MaintenanceMode OR (TRIM(COALESCE(r.schedule_id,''))<>'' AND EXISTS(SELECT 1 FROM schedule s WHERE s.id=r.schedule_id) AND NOT EXISTS(SELECT 1 FROM run current_run WHERE current_run.id=r.id AND COALESCE(current_run.run_kind,'execution')='execution')))
    #if($MaintenanceMode)
    AND (${View.TimestampSecondsUTC("COALESCE(r.completed_at,r.updated_at,r.created_at)")} < $MaintenanceBeforeSecond OR (${View.TimestampSecondsUTC("COALESCE(r.completed_at,r.updated_at,r.created_at)")} = $MaintenanceBeforeSecond AND ${View.TimestampNanoseconds("COALESCE(r.completed_at,r.updated_at,r.created_at)")} <= $MaintenanceBeforeNano))
    #if($Has.MaintenanceAfterId)
    AND (${View.TimestampSecondsUTC("COALESCE(r.completed_at,r.updated_at,r.created_at)")} > $MaintenanceAfterActivity OR (${View.TimestampSecondsUTC("COALESCE(r.completed_at,r.updated_at,r.created_at)")} = $MaintenanceAfterActivity AND (${View.TimestampNanoseconds("COALESCE(r.completed_at,r.updated_at,r.created_at)")} > $MaintenanceAfterNano OR (${View.TimestampNanoseconds("COALESCE(r.completed_at,r.updated_at,r.created_at)")} = $MaintenanceAfterNano AND HEX(r.id)>HEX($MaintenanceAfterId)))))
    #end
    #end
    ORDER BY 
       
    #if($MaintenanceMode)
       ${View.TimestampSecondsUTC("COALESCE(r.completed_at,r.updated_at,r.created_at)")} ASC,${View.TimestampNanoseconds("COALESCE(r.completed_at,r.updated_at,r.created_at)")} ASC,
    #end
       
       CASE WHEN $MaintenanceMode THEN HEX(r.id) END ASC,
       CASE WHEN NOT $MaintenanceMode THEN r.created_at END,CASE WHEN NOT $MaintenanceMode THEN r.id END
