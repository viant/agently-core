SELECT data_rows.* FROM  (
    SELECT CAST(COALESCE(j.completed_at,j.started_at,j.submitted_at) AS CHAR) AS maintenance_observed_at, j.job_id, j.artifact_ref, j.owner_id,
           COALESCE(j.conversation_id,'') AS conversation_id,
           COALESCE(j.workspace_id,'') AS workspace_id,
           COALESCE(j.auth_context_ref,'') AS auth_context_ref,
           j.format, j.scope, j.status, j.report_spec_json,
           j.report_fill_json, j.report_print_json, j.metadata_json,
           COALESCE(j.artifact_id,'') AS artifact_id,
           COALESCE(j.error_text,'') AS error_text, j.diagnostics_json,
           j.submitted_at, j.started_at, j.completed_at,
           j.retention_ttl_sec, COALESCE(j.report_run_id,'') AS report_run_id,
           COALESCE(j.report_run_revision,0) AS report_run_revision,
           COALESCE(j.export_request_id,'') AS export_request_id
    FROM report_export_job j
    WHERE ($Internal OR j.owner_id = $OwnerSubject)
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY j.submitted_at DESC, j.job_id DESC
)  data_rows