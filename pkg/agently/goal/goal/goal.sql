( SELECT t.*
  FROM goal t
  ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
  LIMIT 1 )
