SELECT t.*
 FROM model_call t
 JOIN message m ON m.id = t.message_id
 WHERE m.role = 'assistant'
 ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("AND")}
