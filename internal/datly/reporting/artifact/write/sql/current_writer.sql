SELECT r.artifact_id, r.job_id, r.artifact_ref, r.owner_id, r.format, r.content_type, r.inline_data, r.created_at, r.retention_ttl_sec FROM (SELECT data_rows.artifact_id, data_rows.should_delete, data_rows.job_id, data_rows.artifact_ref, data_rows.owner_id, data_rows.format, data_rows.content_type, data_rows.inline_data, data_rows.created_at, data_rows.retention_ttl_sec FROM  (
    SELECT 0 AS should_delete, artifact_id, job_id, artifact_ref,
           owner_id, format, content_type, inline_data, created_at,
           retention_ttl_sec
    FROM report_export_artifact
)  data_rows) r WHERE $criteria.CompositeIn("r", $WriterKeys)