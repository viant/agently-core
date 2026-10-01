SELECT session.* FROM  (
SELECT CAST(s.expires_at AS CHAR) AS maintenance_observed_at, s.*, u.username, u.display_name, u.email, u.subject
 FROM (
   SELECT s.* FROM session s
   ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
 ) s
 LEFT JOIN users u ON u.id=s.user_id
)  session