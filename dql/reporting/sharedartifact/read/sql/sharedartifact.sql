
 SELECT r.artifact_id, r.artifact_ref, r.owner_id, COALESCE(r.owner_ref, '') AS owner_ref, r.kind, r.lifecycle, r.version, COALESCE(r.report_id, '') AS report_id, COALESCE(r.title, '') AS title, COALESCE(r.source_artifact_id, '') AS source_artifact_id, COALESCE(r.base_artifact_ref, '') AS base_artifact_ref, COALESCE(r.policy_ref, '') AS policy_ref, r.document_version, r.report_document_json, r.report_spec_json, r.compile_state_json, r.report_fill_json, r.report_print_json, r.saved_view_overlay_json, r.metadata_json, r.created_at, r.updated_at FROM report_shared_artifact r WHERE 1=1
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0,"AND")).Build("AND")}
 AND ($Internal OR r.owner_id=NULLIF($OwnerSubject,''))
 AND ($ReadMode<>'byId' OR r.artifact_id=$ArtifactID)
 ORDER BY CASE WHEN $ReadMode='rows' THEN COALESCE(r.updated_at,r.created_at) END DESC,
 CASE WHEN $ReadMode='rows' THEN r.artifact_id END DESC
