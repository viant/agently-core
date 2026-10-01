SELECT session.maintenance_observed_at, session.id, session.user_id, session.provider, session.created_at, session.updated_at, session.expires_at, session.username, session.display_name, session.email, session.subject FROM  (
SELECT CAST(s.expires_at AS CHAR) AS maintenance_observed_at, s.*, u.username, u.display_name, u.email, u.subject
 FROM (
   SELECT s.* FROM session s
   ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
   #if($LockRows) ${View.ForUpdate()} #end  
 ) s
 LEFT JOIN users u ON u.id=s.user_id
)  session