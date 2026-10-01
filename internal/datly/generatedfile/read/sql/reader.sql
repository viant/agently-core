SELECT generated_file.id, generated_file.conversation_id, generated_file.turn_id, generated_file.message_id, generated_file.provider, generated_file.mode, generated_file.copy_mode, generated_file.status, generated_file.payload_id, generated_file.container_id, generated_file.provider_file_id, generated_file.filename, generated_file.mime_type, generated_file.size_bytes, generated_file.checksum, generated_file.error_message, generated_file.expires_at, generated_file.created_at, generated_file.updated_at FROM  (
SELECT
  gf.id,
  gf.conversation_id,
  gf.turn_id,
  gf.message_id,
  gf.provider,
  gf.mode,
  gf.copy_mode,
  gf.status,
  gf.payload_id,
  gf.container_id,
  gf.provider_file_id,
  gf.filename,
  gf.mime_type,
  gf.size_bytes,
  gf.checksum,
  gf.error_message,
  gf.expires_at,
  gf.created_at,
  gf.updated_at
FROM generated_file gf
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY gf.created_at ASC

#if($LockRows) ${View.ForUpdate()} #end  
)  generated_file