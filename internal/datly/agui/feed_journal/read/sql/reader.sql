SELECT * FROM  (SELECT p.id,p.run_id,r.protocol_key,r.protocol_run_id,c.protocol_thread_id,r.effective_user_id,p.sequence,p.inline_body
FROM run r JOIN conversation c ON c.id=r.conversation_id JOIN call_payload p ON p.run_id=r.id AND p.kind='agui.event'
WHERE r.run_kind='agui' AND r.effective_user_id=$Principal AND c.protocol_thread_key=$ThreadKey AND c.protocol_principal=$Principal
 AND (r.protocol_key>$AfterKey OR (r.protocol_key=$AfterKey AND p.sequence>$AfterSequence))
ORDER BY r.protocol_key,p.sequence LIMIT 256
)  agui_data