SELECT payload_rows.id, payload_rows.tenant_id, payload_rows.kind, payload_rows.subtype, payload_rows.mime_type, payload_rows.size_bytes, payload_rows.digest, payload_rows.storage, payload_rows.inline_body, payload_rows.uri, payload_rows.compression, payload_rows.encryption_kms_key_id, payload_rows.redaction_policy_version, payload_rows.redacted, payload_rows.created_at, payload_rows.schema_ref FROM  (
SELECT
    p.id,
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
)  payload_rows
#if($LockRows) ${View.ForUpdate()} #end