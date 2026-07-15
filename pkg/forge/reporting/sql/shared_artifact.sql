( SELECT r.*
  FROM report_shared_artifact r
  ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")} )
