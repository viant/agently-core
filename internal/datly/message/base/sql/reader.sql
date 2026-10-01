SELECT data_rows.id, data_rows.conversation_id, data_rows.turn_id, data_rows.archived, data_rows.sequence, data_rows.created_at, data_rows.updated_at, data_rows.created_by_user_id, data_rows.status, data_rows.mode, data_rows.role, data_rows.type, data_rows.content, data_rows.raw_content, data_rows.summary, data_rows.context_summary, data_rows.tags, data_rows.interim, data_rows.elicitation_id, data_rows.parent_message_id, data_rows.superseded_by, data_rows.linked_conversation_id, data_rows.attachment_payload_id, data_rows.elicitation_payload_id, data_rows.tool_name, data_rows.embedding_index, data_rows.preamble, data_rows.iteration, data_rows.phase FROM  (
 SELECT $ReadMode AS read_mode, m.*, LOWER(TRIM(COALESCE(m.status,''))) AS cleanup_status, COALESCE(p.inline_body,'') AS elicitation_body,
 COALESCE(p.compression,'none') AS elicitation_compression, '' AS elicitation
 FROM message m LEFT JOIN call_payload p ON p.id=m.elicitation_payload_id
 WHERE 1=1
 ${predicate.Builder().CombineOr($predicate.FilterGroup(4,"AND")).Build("AND")}
 AND ($Internal OR EXISTS(SELECT 1 FROM conversation c WHERE c.id=m.conversation_id
   AND (COALESCE(c.visibility,'')<>'private' OR c.created_by_user_id=NULLIF($VisibilitySubject,''))))
 AND ($ReadMode NOT IN ('byId','transcript') OR m.id=$Id)
 AND ($ReadMode<>'elicitation' OR (m.conversation_id=$ConversationId AND m.elicitation_id=$ElicitationId AND m.type<>'elicitation_response'))
 AND ($ReadMode<>'linkedElicitation' OR (m.linked_conversation_id=$LinkedConversationId AND m.elicitation_id=$ElicitationId))
 AND ($ReadMode<>'parentElicitation' OR (m.parent_message_id=$ParentMessageId AND m.elicitation_id=$ElicitationId))
 ORDER BY CASE WHEN $ReadMode='elicitation' THEN m.created_at END DESC
)  data_rows