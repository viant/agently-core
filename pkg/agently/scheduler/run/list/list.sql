( SELECT t.* FROM run t
     WHERE t.schedule_id IS NOT NULL
       AND EXISTS (
         SELECT 1
         FROM schedule s
         WHERE s.id = t.schedule_id
           AND (s.visibility IS NULL OR s.visibility <> 'private' OR s.created_by_user_id = $EffectiveUserID)
     )
     ${predicate.Builder().CombineOr($predicate.FilterGroup(0, "AND")).Build("AND")}
     ORDER BY started_at DESC, id DESC
     LIMIT $Limit OFFSET $Offset )
