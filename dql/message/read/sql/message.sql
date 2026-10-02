
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
