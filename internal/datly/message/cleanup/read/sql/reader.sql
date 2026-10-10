SELECT metadata.* FROM  (SELECT t.id, t.conversation_id, t.linked_conversation_id, t.parent_message_id, t.superseded_by, t.attachment_payload_id, t.elicitation_payload_id
FROM message t
${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
ORDER BY t.id
)  metadata