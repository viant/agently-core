(SELECT m.*,
    '' elicitation,
    COALESCE(p.inline_body, '')   elicitation_body,
    COALESCE(p.compression, 'none') elicitation_compression
    FROM `message` m
    LEFT JOIN call_payload p ON m.elicitation_payload_id = p.id
   ${predicate.Builder().CombineOr($predicate.FilterGroup(4, "AND")).Build("WHERE")} )