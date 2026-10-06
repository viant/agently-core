SELECT r.protocol_key,r.protocol_run_id,c.protocol_thread_id,r.effective_user_id,r.protocol_turn_id,r.conversation_id,n.security_context
FROM run r JOIN conversation c ON c.id=r.conversation_id
LEFT JOIN turn t ON t.id=r.protocol_turn_id AND t.conversation_id=r.conversation_id
LEFT JOIN run n ON n.id=t.run_id AND n.run_kind='execution'
WHERE r.run_kind='agui' AND r.protocol_key>$AfterKey AND (
 (r.protocol_status='interrupted' AND COALESCE(r.protocol_resumed_by_run_id,'')='') OR
 (r.protocol_status IN ('admitted','running') AND (COALESCE(r.protocol_prior_run_id,'')<>'' OR r.protocol_turn_id IS NULL OR r.protocol_turn_id='') AND (r.protocol_lease_until IS NULL OR r.protocol_lease_until<=$Before)))
ORDER BY r.protocol_key LIMIT 50
