SELECT t.*
 FROM model_call t
 ${predicate.Builder().CombineOr($predicate.FilterGroup(2, "AND")).Build("WHERE")}