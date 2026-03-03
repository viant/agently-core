SELECT t.*
 FROM tool_call t
 ${predicate.Builder().CombineOr($predicate.FilterGroup(3, "AND")).Build("WHERE")}