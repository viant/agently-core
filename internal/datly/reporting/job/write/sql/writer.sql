SELECT data_rows.job_id, data_rows.should_delete, data_rows.artifact_ref, data_rows.owner_id, data_rows.conversation_id, data_rows.workspace_id, data_rows.auth_context_ref, data_rows.format, data_rows.scope, data_rows.status, data_rows.report_spec_json, data_rows.report_fill_json, data_rows.report_print_json, data_rows.metadata_json, data_rows.artifact_id, data_rows.error_text, data_rows.diagnostics_json, data_rows.submitted_at, data_rows.started_at, data_rows.completed_at, data_rows.retention_ttl_sec, data_rows.report_run_id, data_rows.report_run_revision, data_rows.export_request_id FROM  (
    SELECT 0 AS should_delete, job_id, artifact_ref, owner_id, conversation_id,
           workspace_id, auth_context_ref, format, scope, status,
           report_spec_json, report_fill_json, report_print_json, metadata_json,
           artifact_id, error_text, diagnostics_json, submitted_at, started_at,
           completed_at, retention_ttl_sec, report_run_id,
           report_run_revision, export_request_id
    FROM report_export_job
)  data_rows