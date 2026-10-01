SELECT modelCallResponsePayload.id, modelCallResponsePayload.inline_body, modelCallResponsePayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("WHERE")}
) modelCallResponsePayload