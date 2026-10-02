
SELECT t.state_hash,
           t.flow_hash,
           t.user_id,
           t.session_hash,
           t.provider,
           t.expires_at,
           t.consumed_at,
           t.created_at
    FROM oauth_link_state t
    WHERE $Trusted AND (
      ($Mode<>'expired' AND (($FlowHash<>'' AND t.flow_hash=$FlowHash) OR ($StateHash<>'' AND t.state_hash=$StateHash))
        AND (NOT $Pending OR t.consumed_at IS NULL))
      OR ($Mode='expired' AND $Before<>'' AND t.expires_at <= $Before))
    ORDER BY t.expires_at, t.flow_hash
