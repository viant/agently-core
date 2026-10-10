SELECT t.id, t.conversation_id, t.run_id, t.started_by_message_id, t.retry_of
FROM turn t
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY t.id
