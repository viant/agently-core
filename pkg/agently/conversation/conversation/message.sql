SELECT m.*,
           NULL AS ELICITATION
      FROM message m
    ${predicate.Builder().CombineOr($predicate.FilterGroup(4, "AND")).Build("WHERE")}
