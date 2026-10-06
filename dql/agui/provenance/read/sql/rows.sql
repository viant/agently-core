SELECT c.id AS thread_id,t.id AS native_turn_id,c.conversation_parent_id AS parent_thread_id,c.conversation_parent_turn_id AS parent_turn_id,
 CASE WHEN EXISTS(SELECT 1 FROM run r WHERE r.run_kind='agui' AND r.protocol_turn_id=$TurnID AND (r.conversation_id=c.id OR t.id IS NOT NULL)) THEN 1 ELSE 0 END AS agui_owned
FROM conversation c LEFT JOIN turn t ON t.conversation_id=c.id AND t.id=$TurnID WHERE c.id=$ThreadID
