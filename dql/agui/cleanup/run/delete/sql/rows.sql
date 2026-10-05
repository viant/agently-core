SELECT c.id,0 AS should_delete FROM run c WHERE c.run_kind='agui' ${predicate.Builder().CombineAnd($predicate.FilterGroup(0, "AND")).Build("AND")}
