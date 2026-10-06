SELECT * FROM  (SELECT c.id, c.created_by_user_id, c.protocol_thread_key, c.protocol_thread_id, c.protocol_principal, c.protocol_revision, c.protocol_state_json, c.protocol_messages_json, c.protocol_only FROM conversation c WHERE
#if($Has.NativeID)
c.id = $NativeID AND c.created_by_user_id = $Principal
#else
c.protocol_thread_key = $Key AND c.protocol_principal = $Principal
#end
)  agui_data