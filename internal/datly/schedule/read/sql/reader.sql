SELECT schedule.* FROM  (
SELECT t.*, CAST(t.lease_until AS CHAR) AS lease_until_raw FROM schedule t WHERE 1=1
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
     AND ($InternalMode OR (COALESCE(t.internal,0)=0 AND
         (COALESCE(t.visibility,'') <> 'private' OR t.created_by_user_id=NULLIF($VisibilitySubject,''))))
)  schedule