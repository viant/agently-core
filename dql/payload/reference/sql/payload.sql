
SELECT
  p.id,
  CASE WHEN $CheckReferences AND (
    EXISTS (SELECT 1 FROM message WHERE attachment_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM message WHERE elicitation_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE provider_request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE provider_response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE stream_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM tool_call WHERE request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM tool_call WHERE response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM generated_file WHERE payload_id=p.id)
  ) THEN 1 ELSE 0 END AS referenced,
  p.tenant_id,
  p.kind,
  p.subtype,
  p.mime_type,
  p.size_bytes,
  p.digest,
  p.storage,
  p.inline_body,
  p.uri,
  p.compression,
  p.encryption_kms_key_id,
  p.redaction_policy_version,
  p.redacted,
  p.created_at,
  p.schema_ref
FROM call_payload p
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
