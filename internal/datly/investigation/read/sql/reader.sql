SELECT data_rows.* FROM  (
    SELECT i.id, i.title, i.created_by, i.conversation_id,
           i.summary, i.ad_order_id, i.verdict, i.created
    FROM investigation i
    WHERE $Trusted
    ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
    ORDER BY i.created, i.id
)  data_rows