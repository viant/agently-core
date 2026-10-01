SELECT data_rows.* FROM  (
    SELECT a.artifact_id, a.job_id, a.artifact_ref, a.owner_id,
           a.format, a.content_type, a.inline_data, a.created_at,
           a.retention_ttl_sec
    FROM report_export_artifact a
    WHERE ($Internal OR a.owner_id = $OwnerSubject)
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY a.created_at DESC, a.artifact_id DESC
)  data_rows