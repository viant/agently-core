SELECT data_rows.report_run_id, data_rows.should_delete, data_rows.owner_id, data_rows.conversation_id, data_rows.materializer, data_rows.origin, data_rows.builder_ref, data_rows.preset_id, data_rows.source_kind, data_rows.source_id, data_rows.requested_params_json, data_rows.effective_params_json, data_rows.status, data_rows.failure_code, data_rows.failure_text, data_rows.started_at, data_rows.completed_at, data_rows.revision, data_rows.ui_run_request_id, data_rows.report_spec_json, data_rows.report_fill_json, data_rows.report_print_json, data_rows.activation_source, data_rows.adoption_source, data_rows.actor_id, data_rows.created_at, data_rows.updated_at FROM  (
    SELECT 0 AS should_delete, report_run_id, owner_id, conversation_id,
           materializer, origin, builder_ref, preset_id, source_kind, source_id,
           requested_params_json, effective_params_json, status, failure_code,
           failure_text, started_at, completed_at, revision, ui_run_request_id,
           report_spec_json, report_fill_json, report_print_json,
           activation_source, adoption_source, actor_id, created_at, updated_at
    FROM report_run
)  data_rows