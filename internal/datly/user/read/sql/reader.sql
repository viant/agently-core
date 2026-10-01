SELECT user.created_at, user.default_agent_ref, user.default_embedder_ref, user.default_model_ref, user.disabled, user.display_name, user.email, user.hash_ip, user.id, user.provider, user.settings, user.subject, user.timezone, user.updated_at, user.username FROM  (
SELECT t.*  FROM users t
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
)  user