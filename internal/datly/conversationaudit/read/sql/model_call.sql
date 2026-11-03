SELECT modelCall.message_id, modelCall.turn_id, modelCall.provider, modelCall.model, modelCall.model_kind, modelCall.status, modelCall.error_code, modelCall.error_message, modelCall.prompt_tokens, modelCall.prompt_cached_tokens, modelCall.completion_tokens, modelCall.total_tokens, modelCall.prompt_audio_tokens, modelCall.completion_reasoning_tokens, modelCall.completion_audio_tokens, modelCall.completion_accepted_prediction_tokens, modelCall.completion_rejected_prediction_tokens, modelCall.finish_reason, modelCall.started_at, modelCall.completed_at, modelCall.latency_ms, modelCall.cost, modelCall.trace_id, modelCall.span_id, modelCall.request_payload_id, modelCall.response_payload_id, modelCall.provider_request_payload_id, modelCall.provider_response_payload_id, modelCall.stream_payload_id, modelCall.run_id, modelCall.iteration FROM (SELECT modelCall.* FROM (
 SELECT m.* FROM model_call m JOIN message mm ON mm.id=m.message_id
 JOIN turn t ON t.id=mm.turn_id JOIN conversation c ON c.id=t.conversation_id
 WHERE $Detail=1 AND $Allowed=1 AND ($OwnerID='' OR (c.created_by_user_id=$OwnerID AND HEX(c.created_by_user_id)=HEX($OwnerID)))
 AND ($OwnerIDContains='' OR INSTR(LOWER(c.created_by_user_id),LOWER($OwnerIDContains))>0)
 AND ($ConversationID='' OR c.id=$ConversationID) AND ($TurnID='' OR t.id=$TurnID)
 ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
) modelCall
) modelCall