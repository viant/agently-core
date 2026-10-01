SELECT r.claim_key, r.rule_id, r.canonical_tool_name, r.turn_id, r.semantic_request_hash, r.state, r.created_at, r.updated_at, r.finished_at FROM (SELECT data_rows.claim_key, data_rows.should_delete, data_rows.rule_id, data_rows.canonical_tool_name, data_rows.turn_id, data_rows.semantic_request_hash, data_rows.state, data_rows.created_at, data_rows.updated_at, data_rows.finished_at FROM  (
    SELECT 0 AS should_delete, claim_key, rule_id, canonical_tool_name,
           turn_id, semantic_request_hash, state, created_at, updated_at, finished_at
    FROM tool_execution_claim
)  data_rows) r WHERE $criteria.CompositeIn("r", $WriterKeys)