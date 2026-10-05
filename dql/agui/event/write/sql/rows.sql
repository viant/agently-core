SELECT p.id, p.run_id, p.sequence, p.kind, p.mime_type, p.storage, p.size_bytes, p.digest, p.compression, p.inline_body FROM call_payload p WHERE p.kind='agui.event'
