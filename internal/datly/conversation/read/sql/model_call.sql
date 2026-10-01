SELECT modelCall.completed_at, modelCall.completion_accepted_prediction_tokens, modelCall.completion_audio_tokens, modelCall.completion_reasoning_tokens, modelCall.completion_rejected_prediction_tokens, modelCall.completion_tokens, modelCall.cost, modelCall.error_code, modelCall.error_message, modelCall.finish_reason, modelCall.iteration, modelCall.latency_ms, modelCall.message_id, modelCall.model, modelCall.model_kind, modelCall.prompt_audio_tokens, modelCall.prompt_cached_tokens, modelCall.prompt_tokens, modelCall.provider, modelCall.provider_request_payload_id, modelCall.provider_response_payload_id, modelCall.request_payload_id, modelCall.response_payload_id, modelCall.run_id, modelCall.span_id, modelCall.started_at, modelCall.status, modelCall.stream_payload_id, modelCall.total_tokens, modelCall.trace_id, modelCall.turn_id FROM (
SELECT t.*
 FROM model_call t
 JOIN message m ON m.id = t.message_id
 WHERE m.role = 'assistant'
 ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("AND")}
) modelCall