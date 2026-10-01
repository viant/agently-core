SELECT responsePayload.id, responsePayload.inline_body, responsePayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("WHERE")}
) responsePayload