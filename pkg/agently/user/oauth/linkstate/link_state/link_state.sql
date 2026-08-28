( SELECT t.state_hash,
           t.flow_hash,
           t.user_id,
           t.session_hash,
           t.provider,
           t.expires_at,
           t.consumed_at,
           t.created_at
    FROM oauth_link_state t
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")} )