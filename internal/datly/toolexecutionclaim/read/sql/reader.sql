SELECT claims.* FROM  (
    SELECT c.claim_key, c.rule_id, c.canonical_tool_name, c.turn_id,
           c.semantic_request_hash, c.state, c.created_at, c.updated_at, c.finished_at
    FROM tool_execution_claim c
    WHERE $Trusted
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY c.created_at, c.claim_key
)  claims