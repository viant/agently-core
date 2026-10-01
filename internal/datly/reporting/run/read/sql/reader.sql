SELECT data_rows.* FROM  (
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
)  data_rows