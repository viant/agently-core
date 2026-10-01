SELECT messageRequestPayload.id, messageRequestPayload.inline_body, messageRequestPayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("WHERE")}
) messageRequestPayload