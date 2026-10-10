SELECT metadata.* FROM  (SELECT t.message_id, t.turn_id, t.run_id, t.request_payload_id, t.response_payload_id, t.provider_request_payload_id, t.provider_response_payload_id, t.stream_payload_id
FROM model_call t
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY t.message_id
)  metadata