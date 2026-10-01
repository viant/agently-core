SELECT messageResponsePayload.id, messageResponsePayload.inline_body, messageResponsePayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("WHERE")}
) messageResponsePayload