SELECT modelCallProviderRequestPayload.id, modelCallProviderRequestPayload.inline_body, modelCallProviderRequestPayload.compression FROM (
SELECT id, inline_body, compression FROM call_payload ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("WHERE")}
) modelCallProviderRequestPayload