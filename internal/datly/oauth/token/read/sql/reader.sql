SELECT token.created_at, token.enc_token, token.provider, token.updated_at, token.user_id, token.version, token.lease_owner, token.lease_until, token.refresh_status, token.db_now FROM  (
SELECT t.*, ${criteria.UTCNow()} AS db_now FROM user_oauth_token t
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
)  token