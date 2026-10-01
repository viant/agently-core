SELECT modelCallProviderResponsePayload.id, modelCallProviderResponsePayload.inline_body, modelCallProviderResponsePayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("WHERE")}
) modelCallProviderResponsePayload