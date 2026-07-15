( SELECT r.*
  FROM report_shared_artifact r
  ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("WHERE")}
  ORDER BY COALESCE(r.updated_at, r.created_at) DESC, r.artifact_id DESC )
