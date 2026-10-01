SELECT data_rows.event_id, data_rows.should_delete, data_rows.event_type, data_rows.artifact_ref, data_rows.version, data_rows.job_id, data_rows.artifact_id, data_rows.actor_id, data_rows.actor_ref, data_rows.occurred_at, data_rows.metadata_json FROM  (
    SELECT 0 AS should_delete, event_id, event_type, artifact_ref,
           version, job_id, artifact_id, actor_id, actor_ref,
           occurred_at, metadata_json
    FROM report_audit_event
)  data_rows