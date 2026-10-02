
SELECT inline_body, compression, m.id as message_id FROM message m
 JOIN call_payload p ON m.elicitation_payload_id = p.id
 WHERE ($Internal OR EXISTS(SELECT 1 FROM conversation scope_c WHERE scope_c.id=m.conversation_id AND (COALESCE(scope_c.visibility,'')<>'private' OR scope_c.created_by_user_id=NULLIF($VisibilitySubject,'')))) AND m.elicitation_payload_id IS NOT NULL
