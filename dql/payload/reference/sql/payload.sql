
SELECT
  p.*,
  CASE WHEN $CheckReferences AND (
    EXISTS (SELECT 1 FROM message WHERE attachment_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM message WHERE elicitation_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE provider_request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE provider_response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM model_call WHERE stream_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM tool_call WHERE request_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM tool_call WHERE response_payload_id=p.id) OR
    EXISTS (SELECT 1 FROM generated_file WHERE payload_id=p.id) OR
    EXISTS (SELECT 1 FROM run payload_run WHERE payload_run.id=p.run_id)
  ) THEN 1 ELSE 0 END AS referenced
FROM call_payload p
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
