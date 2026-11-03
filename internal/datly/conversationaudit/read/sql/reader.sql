SELECT reader.id, reader.conversation_id, reader.created_at, reader.status, reader.goal_id, reader.queue_seq, reader.origin, reader.status_reason, reader.feedback_rating, reader.feedback_comment, reader.feedback_updated_at, reader.error_message, reader.started_by_message_id, reader.retry_of, reader.agent_id_used, reader.agent_config_used_id, reader.model_override_provider, reader.model_override, reader.model_params_override, reader.run_id, reader.owner_id, reader.conversation_title, reader.conversation_created_at, reader.model_calls, reader.tool_calls, reader.input_tokens, reader.output_tokens, reader.cost, reader.input_preview, reader.output_preview, reader.known_input_tokens, reader.known_output_tokens, reader.known_costs FROM  (SELECT reader.* FROM (
 SELECT t.*,
 (SELECT COUNT(*) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS model_calls,
 (SELECT COUNT(mc.prompt_tokens) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS known_input_tokens,
 (SELECT COUNT(mc.completion_tokens) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS known_output_tokens,
 (SELECT COUNT(mc.cost) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS known_costs,
 (SELECT COUNT(*) FROM tool_call tc JOIN message mm ON mm.id=tc.message_id WHERE mm.turn_id=t.id) AS tool_calls,
 (SELECT SUM(mc.prompt_tokens) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS input_tokens,
 (SELECT SUM(mc.completion_tokens) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS output_tokens,
 (SELECT SUM(mc.cost) FROM model_call mc JOIN message mm ON mm.id=mc.message_id WHERE mm.turn_id=t.id) AS cost,
 (SELECT SUBSTR(COALESCE(m.raw_content,m.content),1,160) FROM message m WHERE m.turn_id=t.id AND m.role='user' AND m.interim=0 ORDER BY m.sequence,m.created_at,m.id LIMIT 1) AS input_preview,
 (SELECT SUBSTR(m.content,1,160) FROM message m WHERE m.turn_id=t.id AND m.role='assistant' AND m.interim=0 AND (m.mode IS NULL OR m.mode<>'router') AND m.superseded_by IS NULL ORDER BY m.sequence DESC,m.created_at DESC,m.id DESC LIMIT 1) AS output_preview
 FROM (
 SELECT t.id,t.conversation_id,t.created_at,t.queue_seq,t.status,t.goal_id,t.origin,t.status_reason,
 t.feedback_rating,t.feedback_comment,t.feedback_updated_at,t.error_message,t.started_by_message_id,
 t.retry_of,t.agent_id_used,t.agent_config_used_id,t.model_override_provider,t.model_override,
 t.model_params_override,t.run_id,
 COALESCE(c.created_by_user_id,'') AS owner_id,c.title AS conversation_title,c.created_at AS conversation_created_at
 FROM turn t JOIN conversation c ON c.id=t.conversation_id
 WHERE $Allowed = 1 AND ($OwnerID = '' OR (c.created_by_user_id=$OwnerID AND HEX(c.created_by_user_id)=HEX($OwnerID)))
 AND ($OwnerIDContains = '' OR INSTR(LOWER(c.created_by_user_id),LOWER($OwnerIDContains)) > 0)
 AND ($ConversationID = '' OR c.id=$ConversationID)
 AND ($TurnID = '' OR t.id=$TurnID)
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
 AND ($ConversationID <> '' OR (c.created_at >= $From AND c.created_at < $To))
 AND ($CursorID = '' OR EXISTS (SELECT 1 FROM turn x WHERE x.id=$CursorID AND (t.created_at<x.created_at OR (t.created_at=x.created_at AND t.id<x.id))))
 ORDER BY t.created_at DESC,t.id DESC
 LIMIT 201
 ) t
 ORDER BY t.created_at DESC,t.id DESC
) reader
)  reader