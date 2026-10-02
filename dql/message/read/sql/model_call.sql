
SELECT t.*
 FROM model_call t
 JOIN message m ON m.id = t.message_id
 WHERE ($Internal OR EXISTS(SELECT 1 FROM conversation scope_c WHERE scope_c.id=m.conversation_id AND (COALESCE(scope_c.visibility,'')<>'private' OR scope_c.created_by_user_id=NULLIF($VisibilitySubject,'')))) AND m.role = 'assistant'
 ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("AND")}
