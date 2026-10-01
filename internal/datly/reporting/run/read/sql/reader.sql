SELECT data_rows.maintenance_observed_at, data_rows.report_run_id, data_rows.owner_id, data_rows.conversation_id, data_rows.materializer, data_rows.origin, data_rows.builder_ref, data_rows.preset_id, data_rows.source_kind, data_rows.source_id, data_rows.requested_params_json, data_rows.effective_params_json, data_rows.status, data_rows.failure_code, data_rows.failure_text, data_rows.started_at, data_rows.completed_at, data_rows.revision, data_rows.ui_run_request_id, data_rows.report_spec_json, data_rows.report_fill_json, data_rows.report_print_json, data_rows.activation_source, data_rows.adoption_source, data_rows.actor_id, data_rows.created_at, data_rows.updated_at FROM  (
    SELECT CAST(r.updated_at AS CHAR) AS maintenance_observed_at, r.report_run_id, r.owner_id,
           COALESCE(r.conversation_id, '') AS conversation_id,
           r.materializer, COALESCE(r.origin, '') AS origin,
           COALESCE(r.builder_ref, '') AS builder_ref,
           COALESCE(r.preset_id, '') AS preset_id,
           COALESCE(r.source_kind, '') AS source_kind,
           COALESCE(r.source_id, '') AS source_id,
           r.requested_params_json, r.effective_params_json, r.status,
           COALESCE(r.failure_code, '') AS failure_code,
           COALESCE(r.failure_text, '') AS failure_text,
           r.started_at, r.completed_at, r.revision, r.ui_run_request_id,
           r.report_spec_json, r.report_fill_json, r.report_print_json,
           COALESCE(r.activation_source, '') AS activation_source,
           COALESCE(r.adoption_source, '') AS adoption_source,
           COALESCE(r.actor_id, '') AS actor_id, r.created_at, r.updated_at
    FROM report_run r
    WHERE ($Internal OR r.owner_id = $OwnerSubject)
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY r.updated_at DESC, r.report_run_id DESC
 #if($LockRows) ${View.ForUpdate()} #end  
)  data_rows