SELECT linkedConversation.id, linkedConversation.status, linkedConversation.created_at, linkedConversation.updated_at FROM (
SELECT id, status, created_at, updated_at
FROM conversation t
WHERE t.id <> '' AND ($Internal OR COALESCE(t.visibility,'')<>'private' OR t.created_by_user_id=NULLIF($VisibilitySubject,''))
) linkedConversation