SELECT documents.* FROM  (SELECT p.id,p.run_id,p.kind,p.subtype,p.schema_ref,p.mime_type,p.storage,p.compression,p.digest,p.size_bytes,p.inline_body FROM call_payload p
)  documents