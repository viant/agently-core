SELECT oauth_link_state.*
FROM oauth_link_state
WHERE $criteria.CompositeIn("oauth_link_state", $WriteKeys)
${predicate.Builder().CombineAnd($predicate.FilterGroup(7,"AND")).Build("AND")}
