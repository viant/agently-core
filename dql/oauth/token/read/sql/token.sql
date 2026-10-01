
SELECT t.*, ${criteria.UTCNow()} AS db_now FROM user_oauth_token t
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
